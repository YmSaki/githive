package workpackageapp

import (
	"context"
	"errors"
	"sort"

	"github.com/ymsaki/githive/internal/app/entitychain"
	"github.com/ymsaki/githive/internal/app/snapshotread"
	"github.com/ymsaki/githive/internal/app/wptree"
	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/materialize"
	"github.com/ymsaki/githive/internal/core/refspace"
	"github.com/ymsaki/githive/internal/core/wpgraph"
)

// Read sources.
const (
	SourceChain    = "chain"
	SourceSnapshot = "snapshot"
)

func (s *Service) localIDs(ctx context.Context) ([]string, error) {
	tips, err := s.localTips(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(tips))
	for id := range tips {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

// localTips maps every local workpackage ID to its ref OID from one
// for-each-ref, so a read can fold exactly the tips it enumerated.
func (s *Service) localTips(ctx context.Context) (map[string]string, error) {
	entries, err := gitx.New(s.Dir).ForEachRef(ctx, refspace.Root+"/workpackage/")
	if err != nil {
		return nil, err
	}
	tips := map[string]string{}
	for _, e := range entries {
		parsed, err := refspace.Parse(e.Ref)
		if err == nil && parsed.Feature == refspace.FeatureWorkpackage {
			tips[parsed.ID] = e.OID
		}
	}
	return tips, nil
}

type observed struct {
	states  map[string]*materialize.State
	shallow map[string]bool
}

// observe folds every local workpackage ref, falling back to the labelled
// head-tree snapshot for shallow refs (read views only). Both reads use the
// OID enumerated by the one for-each-ref, never a re-resolved ref name, so
// a ref moved mid-read cannot mix two different moments into one view.
func (s *Service) observe(ctx context.Context) (*observed, error) {
	if err := entitychain.CheckSchemaSupported(ctx, s.Dir); err != nil {
		return nil, err
	}
	tips, err := s.localTips(ctx)
	if err != nil {
		return nil, err
	}
	if s.afterRefList != nil {
		s.afterRefList()
	}
	ids := make([]string, 0, len(tips))
	for id := range tips {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	o := &observed{states: map[string]*materialize.State{}, shallow: map[string]bool{}}
	for _, id := range ids {
		oid := tips[id]
		state, err := s.writer.FoldAt(ctx, oid)
		var shallow *chain.ShallowHistoryError
		if errors.As(err, &shallow) {
			state, err = snapshotread.ReadAt(ctx, s.Dir, RefFor(id).String(), oid, feature)
			o.shallow[id] = true
		}
		if err != nil {
			return nil, err
		}
		if state.Meta != nil {
			o.states[id] = state
		}
	}
	return o, nil
}

func (s *Service) derive(ctx context.Context, o *observed) (map[string]wpgraph.Result, error) {
	ledger, _, err := s.ledger(ctx)
	if err != nil {
		return nil, err
	}
	metas := make(map[string]map[string]any, len(o.states))
	for id, st := range o.states {
		metas[id] = st.Meta
	}
	return wpgraph.Derive(wpgraph.Input{WPs: metas, Shallow: o.shallow, Ledger: ledger, Now: s.now()}), nil
}

// Show is one workpackage's full read view.
type Show struct {
	Meta    map[string]any
	Body    string
	Notes   []map[string]any
	Derived map[string]any
	// ReadSource is "chain" or "snapshot".
	ReadSource      string
	HistoryComplete bool
}

// Data renders the --json data object.
func (sh *Show) Data() map[string]any {
	notes := make([]any, len(sh.Notes))
	for i, n := range sh.Notes {
		notes[i] = n
	}
	data := map[string]any{"meta": sh.Meta, "body": sh.Body, "notes": notes,
		"read_source": sh.ReadSource, "history_complete": sh.HistoryComplete}
	for k, v := range sh.Derived {
		data[k] = v
	}
	return data
}

// Show returns meta/body/notes plus the derived graph fields of id.
func (s *Service) Show(ctx context.Context, id string) (*Show, error) {
	o, err := s.observe(ctx)
	if err != nil {
		return nil, err
	}
	state, ok := o.states[id]
	if !ok {
		return nil, ErrNotFound
	}
	results, err := s.derive(ctx, o)
	if err != nil {
		return nil, err
	}
	text, _ := state.Collections["body"]["text"].(string)
	sh := &Show{Meta: state.Meta, Body: text, Derived: results[id].Fields(), ReadSource: SourceChain, HistoryComplete: true}
	if o.shallow[id] {
		sh.ReadSource, sh.HistoryComplete = SourceSnapshot, false
	}
	for _, nid := range wptree.SortedNoteIDs(state.Collections["notes"]) {
		if n, ok := state.Collections["notes"][nid].(map[string]any); ok {
			sh.Notes = append(sh.Notes, n)
		}
	}
	return sh, nil
}

// ListFilter narrows List. Zero value lists everything.
type ListFilter struct {
	Ready, Blocked, InReview bool
	// Mine matches active_claim.holder against Self.
	Mine bool
	Self string
	// Parent matches parent_id (full ID).
	Parent string
}

// ListResult is wp list's data.
type ListResult struct {
	Items           []map[string]any
	HistoryComplete bool
}

// List returns every matching workpackage with its derived fields.
func (s *Service) List(ctx context.Context, filter ListFilter) (*ListResult, error) {
	o, err := s.observe(ctx)
	if err != nil {
		return nil, err
	}
	results, err := s.derive(ctx, o)
	if err != nil {
		return nil, err
	}
	out := &ListResult{Items: []map[string]any{}, HistoryComplete: len(o.shallow) == 0}
	ids := make([]string, 0, len(o.states))
	for id := range o.states {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		meta := o.states[id].Meta
		res := results[id]
		if filter.Ready && res.DerivedStatus != wpgraph.StatusReady ||
			filter.Blocked && res.DerivedStatus != wpgraph.StatusBlocked ||
			filter.InReview && res.DerivedStatus != wpgraph.StatusInReview {
			continue
		}
		if filter.Mine && (res.ActiveClaim == nil || res.ActiveClaim["holder"] != filter.Self) {
			continue
		}
		if filter.Parent != "" && meta["parent_id"] != filter.Parent {
			continue
		}
		item := map[string]any{}
		for k, v := range meta {
			item[k] = v
		}
		for k, v := range res.Fields() {
			item[k] = v
		}
		item["read_source"], item["history_complete"] = SourceChain, true
		if o.shallow[id] {
			item["read_source"], item["history_complete"] = SourceSnapshot, false
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

// ResolveParentFilter resolves wp list --parent: a full ULID may name an
// unobserved parent; prefixes must match a local ref.
func (s *Service) ResolveParentFilter(ctx context.Context, input string) (string, error) {
	return s.resolveSparse(ctx, input)
}
