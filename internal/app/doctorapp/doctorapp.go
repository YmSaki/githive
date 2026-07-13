// Package doctorapp implements `githive doctor`: a read-only diagnosis of
// the local environment (docs/10-cli-spec.md「コマンド体系」: `githive
// doctor` # 環境診断). It answers "is this machine set up correctly to use
// githive" - git version, the tracking refspec (ADR-0008), clock skew
// against tracked remote events (docs/03「時計異常への防御」), git identity
// (ADR-0009), and commit signing config (docs/11). Unlike `githive status`
// (a summary of the project's state), doctor only inspects the environment,
// never the entities, and never writes anything.
package doctorapp

import (
	"context"
	"fmt"
	"time"

	"github.com/go-git/go-git/v5/plumbing"

	"github.com/ymsaki/githive/internal/core/chain"
	"github.com/ymsaki/githive/internal/core/gitx"
	"github.com/ymsaki/githive/internal/core/identity"
	"github.com/ymsaki/githive/internal/core/refspace"
)

// Severity classifies one diagnostic result. error means the environment is
// broken for githive's purposes (doctor exits non-zero); warning means
// degraded-but-usable; ok means fine.
const (
	SeverityOK      = "ok"
	SeverityWarning = "warning"
	SeverityError   = "error"
)

// tsLayout is the RFC3339 UTC millisecond format event envelopes' ts uses
// (docs/02-data-model.md「イベント封筒」, mirrors idgen.NewWithTimestamp).
const tsLayout = "2006-01-02T15:04:05.000Z"

// clockSkewThreshold is the tolerance before a lagging local clock is
// flagged, matching the 5-minute bound docs/03-sync-and-concurrency.md
// 「時計異常への防御」uses for clock-anomaly warnings.
const clockSkewThreshold = 5 * time.Minute

// Check is one environment diagnostic. Name is a stable machine key (used
// as the --json field), Detail is the human-readable explanation.
type Check struct {
	Name     string
	Severity string
	Detail   string
}

// Report is the full set of diagnostics from one Diagnose call.
type Report struct {
	Checks []Check
}

// HasError reports whether any check is at error severity (doctor then
// exits with cliout.ExitEnvironment).
func (r Report) HasError() bool {
	for _, c := range r.Checks {
		if c.Severity == SeverityError {
			return true
		}
	}
	return false
}

// Service diagnoses the environment for the repository at Dir.
type Service struct {
	Dir string
	// nowFn overrides the wall clock for the clock-skew check; nil means
	// time.Now (tests inject a fixed clock).
	nowFn func() time.Time
}

// New returns a Service rooted at dir.
func New(dir string) *Service {
	return &Service{Dir: dir}
}

func (s *Service) now() time.Time {
	if s.nowFn != nil {
		return s.nowFn()
	}
	return time.Now().UTC()
}

// Diagnose runs every check and returns the collected report. remote is the
// git remote name whose tracking refspec is checked (the CLI passes
// --remote, default "origin"). It returns a Go error only for a failure
// that prevents diagnosis entirely; individual check failures are folded
// into the report as error/warning-severity checks so the report is always
// complete.
func (s *Service) Diagnose(ctx context.Context, remote string) (Report, error) {
	checks := []Check{
		s.checkGitVersion(ctx),
		s.checkRefspec(ctx, remote),
		s.checkClockSkew(ctx),
		s.checkIdentity(ctx),
		s.checkSigning(ctx),
	}
	return Report{Checks: checks}, nil
}

// checkGitVersion verifies system git meets gitx.MinVersion.
func (s *Service) checkGitVersion(ctx context.Context) Check {
	v, err := gitx.CheckVersion(ctx)
	if err != nil {
		return Check{Name: "git_version", Severity: SeverityError, Detail: err.Error()}
	}
	return Check{Name: "git_version", Severity: SeverityOK, Detail: fmt.Sprintf("git %s (>= %s)", v, gitx.MinVersion)}
}

// checkRefspec verifies the ADR-0008 tracking refspec
// (+refs/projects/*:refs/githive-remote/*) is installed for remote, so IDE
// auto-fetch lands under refs/githive-remote/ instead of clobbering local
// chains. A missing refspec is a warning, not an error: a local-only repo
// (no remote yet) works fine, it just lacks the auto-fetch safety net until
// `githive init` runs.
func (s *Service) checkRefspec(ctx context.Context, remote string) Check {
	key := fmt.Sprintf("remote.%s.fetch", remote)
	want := fmt.Sprintf("+%s/*:%s/*", refspace.Root, refspace.RemoteTrackingRoot)
	values, err := gitx.New(s.Dir).ConfigGetAll(ctx, key)
	if err != nil {
		return Check{Name: "tracking_refspec", Severity: SeverityWarning, Detail: "could not read " + key + ": " + err.Error()}
	}
	for _, v := range values {
		if v == want {
			return Check{Name: "tracking_refspec", Severity: SeverityOK, Detail: key + " installed"}
		}
	}
	return Check{Name: "tracking_refspec", Severity: SeverityWarning, Detail: fmt.Sprintf("%s does not include %q; run `githive init` (ADR-0008)", key, want)}
}

// checkClockSkew warns when the local clock lags the newest tracked remote
// event by more than clockSkewThreshold, since events created with a
// lagging clock lose last-writer-wins races until the clock catches up
// (docs/03「時計異常への防御」).
func (s *Service) checkClockSkew(ctx context.Context) Check {
	latest, err := s.latestRemoteTS(ctx)
	if err != nil {
		return Check{Name: "clock_skew", Severity: SeverityWarning, Detail: "could not read remote-tracking events: " + err.Error()}
	}
	if latest == "" {
		return Check{Name: "clock_skew", Severity: SeverityOK, Detail: "no remote-tracking events to compare against"}
	}
	latestT, err := time.Parse(tsLayout, latest)
	if err != nil {
		return Check{Name: "clock_skew", Severity: SeverityWarning, Detail: "unparseable remote event timestamp " + latest}
	}
	skew := latestT.Sub(s.now())
	if skew > clockSkewThreshold {
		return Check{
			Name:     "clock_skew",
			Severity: SeverityWarning,
			Detail:   fmt.Sprintf("local clock is %s behind the latest remote event (%s); new events may lose LWW races", skew.Round(time.Second), latest),
		}
	}
	return Check{Name: "clock_skew", Severity: SeverityOK, Detail: "within " + clockSkewThreshold.String() + " of the latest remote event"}
}

// latestRemoteTS returns the maximum event ts across every remote-tracking
// chain (refs/githive-remote/*), or "" if none exist. ts strings share the
// fixed tsLayout, so lexical max equals chronological max.
func (s *Service) latestRemoteTS(ctx context.Context) (string, error) {
	entries, err := gitx.New(s.Dir).ForEachRef(ctx, refspace.RemoteTrackingRoot+"/")
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "", nil
	}
	repo, err := chain.OpenRepository(s.Dir)
	if err != nil {
		return "", err
	}
	var maxTS string
	for _, e := range entries {
		envs, err := chain.WalkChain(repo, plumbing.NewHash(e.OID))
		if err != nil {
			return "", err
		}
		for _, env := range envs {
			if env.TS > maxTS {
				maxTS = env.TS
			}
		}
	}
	return maxTS, nil
}

// checkIdentity verifies git user.email resolves (ADR-0009: it is every
// event's actor). Its absence is an error - no write command can run.
func (s *Service) checkIdentity(ctx context.Context) Check {
	sig, err := identity.Resolve(ctx, s.Dir)
	if err != nil {
		return Check{Name: "identity", Severity: SeverityError, Detail: err.Error()}
	}
	return Check{Name: "identity", Severity: SeverityOK, Detail: "user.email = " + sig.Email}
}

// checkSigning inspects commit-signing config (docs/11). Signing is optional
// (P3), so an unset or incomplete config is a warning, not an error:
// unsigned commits still work, they just get flagged by `githive verify`.
func (s *Service) checkSigning(ctx context.Context) Check {
	r := gitx.New(s.Dir)
	gpgsign, err := r.ConfigGet(ctx, "commit.gpgsign")
	if err != nil {
		return Check{Name: "signing", Severity: SeverityWarning, Detail: "could not read commit.gpgsign: " + err.Error()}
	}
	format, err := r.ConfigGet(ctx, "gpg.format")
	if err != nil {
		return Check{Name: "signing", Severity: SeverityWarning, Detail: "could not read gpg.format: " + err.Error()}
	}
	signingKey, err := r.ConfigGet(ctx, "user.signingkey")
	if err != nil {
		return Check{Name: "signing", Severity: SeverityWarning, Detail: "could not read user.signingkey: " + err.Error()}
	}

	if gpgsign != "true" {
		return Check{Name: "signing", Severity: SeverityWarning, Detail: "commit.gpgsign is not enabled; commits will be unsigned (optional, docs/11)"}
	}
	if format != "ssh" || signingKey == "" {
		return Check{Name: "signing", Severity: SeverityWarning, Detail: "commit.gpgsign is on but gpg.format/user.signingkey are incomplete (want gpg.format=ssh + user.signingkey)"}
	}
	return Check{Name: "signing", Severity: SeverityOK, Detail: "ssh signing configured"}
}
