package workpackageapp

import (
	"context"
	"errors"
	"strings"

	"github.com/go-git/go-git/v5/plumbing"

	"github.com/ymsaki/githive/internal/app/usersapp"
	"github.com/ymsaki/githive/internal/app/verifyapp"
	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/refspace"
	"github.com/ymsaki/githive/internal/core/wpgraph"
)

// ledger classifies users/registry into the 3 categories of the approval
// model and, for a complete healthy registry, returns its users. reason is
// the first corruption code when the state is LedgerInvalid.
func (s *Service) ledger(ctx context.Context) (wpgraph.Ledger, string, error) {
	r := gitx.New(s.Dir)
	oid, err := r.RevParse(ctx, refspace.UsersRegistryRef)
	if err != nil {
		return wpgraph.Ledger{}, "", err
	}
	pin, err := verifyapp.TrustRootPin(ctx, s.Dir)
	if err != nil {
		return wpgraph.Ledger{}, "", err
	}
	if oid == "" {
		tracking, err := refspace.RemoteTrackingRef(refspace.UsersRegistryRef)
		if err != nil {
			return wpgraph.Ledger{}, "", err
		}
		remote, err := r.RevParse(ctx, tracking)
		if err != nil {
			return wpgraph.Ledger{}, "", err
		}
		if remote != "" || pin != "" {
			return wpgraph.Ledger{State: wpgraph.LedgerIncomplete}, "", nil
		}
		return wpgraph.Ledger{State: wpgraph.LedgerNone}, "", nil
	}
	repo, err := chain.OpenRepository(s.Dir)
	if err != nil {
		return wpgraph.Ledger{}, "", err
	}
	var shallow *chain.ShallowHistoryError
	if err := chain.RequireCompleteHistory(repo, plumbing.NewHash(oid)); errors.As(err, &shallow) {
		return wpgraph.Ledger{State: wpgraph.LedgerIncomplete}, "", nil
	} else if err != nil {
		return wpgraph.Ledger{}, "", err
	}
	report, err := verifyapp.VerifyRef(ctx, s.Dir, refspace.UsersRegistryRef, pin)
	if errors.As(err, &shallow) {
		return wpgraph.Ledger{State: wpgraph.LedgerIncomplete}, "", nil
	}
	if err != nil {
		return wpgraph.Ledger{}, "", err
	}
	if len(report.Issues) > 0 {
		return wpgraph.Ledger{State: wpgraph.LedgerInvalid}, report.Issues[0].Code, nil
	}
	users, _, _, err := usersapp.New(s.Dir).List(ctx)
	if err != nil {
		return wpgraph.Ledger{}, "", err
	}
	ledger := wpgraph.Ledger{State: wpgraph.LedgerComplete}
	for _, u := range users {
		name, _ := u["username"].(string)
		status, _ := u["status"].(string)
		var emails []string
		raw, _ := u["emails"].([]any)
		for _, e := range raw {
			if es, ok := e.(string); ok {
				emails = append(emails, es)
			}
		}
		ledger.Users = append(ledger.Users, wpgraph.LedgerUser{Username: name, Active: status == "active", Emails: emails})
	}
	return ledger, "", nil
}

// resolveReviewers applies submit --reviewers resolution. Registry health
// is judged before any individual reviewer.
func (s *Service) resolveReviewers(ctx context.Context, inputs []string) ([]any, error) {
	if len(inputs) == 0 {
		return nil, invalid("submit", "reviewers", "must not be an empty list")
	}
	ledger, reason, err := s.ledger(ctx)
	if err != nil {
		return nil, err
	}
	switch ledger.State {
	case wpgraph.LedgerInvalid:
		return nil, &RegistryInvalidError{Reason: reason}
	case wpgraph.LedgerIncomplete:
		oid, _ := gitx.New(s.Dir).RevParse(ctx, refspace.UsersRegistryRef)
		return nil, &chain.ShallowHistoryError{Head: plumbing.NewHash(oid)}
	}
	out := make([]any, 0, len(inputs))
	for _, input := range inputs {
		hasAt := strings.Contains(input, "@")
		if ledger.State == wpgraph.LedgerNone {
			if !hasAt || !usersapp.ValidEmail(input) {
				return nil, invalid("submit", "reviewers", "without a users registry each reviewer must be an email address")
			}
			out = append(out, input)
			continue
		}
		resolved := ""
		for _, u := range ledger.Users {
			if hasAt {
				for _, e := range u.Emails {
					if e == input {
						resolved = e
					}
				}
			} else if u.Username == input {
				if len(u.Emails) == 0 {
					return nil, invalid("submit", "reviewers", "user "+input+" has no email")
				}
				resolved = u.Emails[0]
			}
		}
		if resolved == "" {
			if hasAt {
				return nil, invalid("submit", "reviewers", "email "+input+" is not registered")
			}
			return nil, usersapp.ErrUserNotFound
		}
		out = append(out, resolved)
	}
	return out, nil
}
