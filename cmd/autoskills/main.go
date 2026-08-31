// autoskills — turn your AI coding sessions into reviewed, committed skills.
//
//	autoskills scan    discover transcripts, distill new sessions into suggestions
//	autoskills review  open the local review dashboard
//	autoskills status  discovery report + suggestion counts
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/elcruzo/autoskills/internal/cache"
	"github.com/elcruzo/autoskills/internal/canon"
	"github.com/elcruzo/autoskills/internal/collector"
	"github.com/elcruzo/autoskills/internal/collector/claude"
	"github.com/elcruzo/autoskills/internal/collector/cursor"
	"github.com/elcruzo/autoskills/internal/config"
	"github.com/elcruzo/autoskills/internal/distill"
	"github.com/elcruzo/autoskills/internal/gitmeta"
	"github.com/elcruzo/autoskills/internal/llm"
	"github.com/elcruzo/autoskills/internal/server"
	"github.com/elcruzo/autoskills/internal/store"
	"github.com/elcruzo/autoskills/internal/writer"
)

const version = "0.1.0"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "scan":
		err = cmdScan(os.Args[2:])
	case "review":
		err = cmdReview(os.Args[2:])
	case "status":
		err = cmdStatus(os.Args[2:])
	case "daemon":
		err = cmdDaemon(os.Args[2:])
	case "install-daemon":
		err = cmdInstallDaemon(os.Args[2:])
	case "garden":
		err = cmdGarden(os.Args[2:])
	case "verify":
		err = cmdVerify(os.Args[2:])
	case "undo":
		err = cmdUndo(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Println("autoskills " + version)
	case "help", "--help", "-h":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`autoskills — turn your AI coding sessions into reviewed, committed skills

usage:
  autoskills scan [--project NAME] [--since DUR] [--max N] [--dry-run]
  autoskills review [--addr HOST:PORT] [--no-open]
  autoskills daemon                     run continuously: auto-scan as sessions finish
  autoskills install-daemon [--uninstall]   install as a launchd service (macOS)
  autoskills garden [--repo PATH]       propose amend/merge/prune for a repo's skills
  autoskills verify [--repo PATH]       report skills referencing paths that no longer exist
  autoskills undo ID                    revert an accepted suggestion (removes the artifact)
  autoskills status
  autoskills version

scan flags:
  --project NAME   only scan sessions whose project name contains NAME
  --since DUR      only sessions modified within DUR (default 720h = 30 days)
  --max N          max sessions to distill this run (default 20)
  --dry-run        parse and report; no LLM calls, nothing stored

config: ~/.autoskills/config.json  (provider, endpoint, api_key, model, trigger_phrase,
        section_budget_bytes, daemon_interval_minutes, …)
        provider: http (default/legacy), codex, or claude
        endpoint must be https, or http on loopback for a local model
        auto_accept_threshold is DEPRECATED and ignored: nothing is written without review
env:    AUTOSKILLS_PROVIDER, AUTOSKILLS_ENDPOINT, AUTOSKILLS_API_KEY, AUTOSKILLS_MODEL
        (falls back to ANTHROPIC_API_KEY / OPENAI_API_KEY)
`)
}

// openStore opens the database and finishes or restores any operation a crash interrupted, before
// the caller can read the store as if it were authoritative. Every command that may mutate goes
// through here; `status` deliberately reports instead of acting.
func openStore() (*store.Store, error) {
	st, err := store.Open(store.DefaultPath())
	if err != nil {
		return nil, err
	}
	report, err := writer.Reconcile(st)
	for _, line := range report {
		fmt.Fprintln(os.Stderr, "reconciled:", line)
	}
	if err != nil {
		st.Close()
		return nil, fmt.Errorf("reconcile interrupted operations: %w", err)
	}
	return st, nil
}

func adapters() ([]collector.Adapter, map[string]string) {
	roots := map[string]string{
		"cursor": collector.HomePath(".cursor", "projects"),
		"claude": collector.HomePath(".claude", "projects"),
	}
	return []collector.Adapter{cursor.New(roots["cursor"]), claude.New(roots["claude"])}, roots
}

func configuredProvider(cfg config.Config) (llm.Provider, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Provider)) {
	case "", "http":
		return llm.New(cfg.Endpoint, cfg.APIKey, cfg.Model)
	case "codex":
		return llm.NewCodex(cfg.Model)
	case "claude":
		return llm.NewClaude(cfg.Model)
	default:
		return nil, fmt.Errorf("unknown LLM provider %q: expected http, codex, or claude", cfg.Provider)
	}
}

func cmdScan(args []string) error {
	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	project := fs.String("project", "", "only scan sessions whose project name contains this")
	since := fs.Duration("since", 720*time.Hour, "only sessions modified within this duration")
	maxSessions := fs.Int("max", 20, "max sessions to distill this run")
	dryRun := fs.Bool("dry-run", false, "parse and report; no LLM calls")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	return runScan(ctx, cfg, st, scanOptions{
		project: *project, since: *since, maxSessions: *maxSessions, dryRun: *dryRun, verbose: true,
	})
}

type scanOptions struct {
	project     string
	since       time.Duration
	maxSessions int
	dryRun      bool
	verbose     bool
}

// runScan is the shared scan engine used by `scan` and `daemon`.
func runScan(ctx context.Context, cfg config.Config, st *store.Store, opts scanOptions) error {
	writer.SectionBudgetBytes = cfg.SectionBudgetBytes

	adapterList, roots := adapters()
	if opts.verbose {
		for _, e := range collector.Discover(adapterList, roots) {
			fmt.Printf("found %s (%d sessions)\n", e.Tool, e.Sessions)
		}
	}

	// Automatic acceptance was removed (HOK-539): a model-authored file write with no human in the
	// loop is not a tuning dial. A configured threshold is honoured as "still parses", never as
	// "still writes" — and the operator is told so on every scan rather than silently ignored.
	if cfg.AutoAcceptThreshold > 0 {
		fmt.Fprintf(os.Stderr,
			"warning: auto_accept_threshold=%.2f in %s is IGNORED — automatic acceptance was removed; every suggestion stays pending until you accept it in `autoskills review`\n",
			cfg.AutoAcceptThreshold, config.Path())
	}

	// Bounded, run-scoped caches (memory stays flat for the always-on daemon):
	//   seenContent  — model-input hashes already distilled, to skip duplicate LLM calls.
	//   fingerprints — normalized title+body of stored suggestions, to suppress near-dupes that
	//                  slip past the exact-title DB check.
	seenContent := cache.New[string, bool](512)
	fingerprints := cache.New[string, bool](2048)

	var d *distill.Distiller
	if !opts.dryRun {
		// endpoint policy is enforced before a client exists: no key travels to an unvetted host
		provider, err := configuredProvider(cfg)
		if err != nil {
			return err
		}
		d = &distill.Distiller{
			Provider:      provider,
			Store:         st,
			MinConfidence: cfg.MinConfidence,
			SeenContent:   seenContent,
		}
	}

	project, maxSessions, dryRun := opts.project, opts.maxSessions, opts.dryRun
	cutoff := time.Now().Add(-opts.since)
	// consecutive hard LLM failures (billing, auth) abort the run instead of hammering the endpoint
	consecutiveFailures := 0
	distilled, stored := 0, 0
	for _, a := range adapterList {
		// the scan cap gates STARTING a session, never truncating a distilled one (a distilled
		// session's suggestions are always stored in full — its high-water mark has advanced)
		if distilled >= maxSessions || stored >= cfg.MaxSuggestionsPerScan {
			break
		}
		files, err := a.SessionFiles()
		if err != nil {
			continue
		}
		for _, f := range files {
			if distilled >= maxSessions || stored >= cfg.MaxSuggestionsPerScan {
				break
			}
			info, err := os.Stat(f)
			if err != nil || info.ModTime().Before(cutoff) {
				continue
			}
			processed, err := st.BytesProcessed(f)
			if err != nil {
				return err
			}
			if processed >= info.Size() {
				continue // high-water mark: nothing new
			}

			sess, err := a.Parse(f)
			if err != nil {
				// parse failures are deterministic — advance the mark so we don't warn forever
				fmt.Fprintf(os.Stderr, "  skip %s: %v\n", f, err)
				_ = st.SetBytesProcessed(f, info.Size())
				continue
			}
			if project != "" && !strings.Contains(strings.ToLower(sess.Project), strings.ToLower(project)) {
				continue
			}
			if ignored(cfg.IgnoreProjects, sess.Project, sess.RepoRoot) {
				_ = st.SetBytesProcessed(f, info.Size())
				continue
			}
			if cfg.TriggerPhrase != "" && !sess.UserSaid(cfg.TriggerPhrase) {
				_ = st.SetBytesProcessed(f, info.Size())
				continue // tuned mode: only distill sessions the user explicitly flagged
			}
			if !worthDistilling(sess) {
				_ = st.SetBytesProcessed(f, info.Size())
				continue
			}
			// Cheap deterministic pre-filter before the LLM: if the transcript shows none of the
			// correction/convention/failure/rediscovery markers, skip the model entirely. The gate
			// is conservative (skips only when NOTHING matches), so recall is preserved.
			kinds, hasSignal := distill.HasSignal(sess)
			if !hasSignal {
				if opts.verbose {
					fmt.Printf("  skip [%s] %s — no distill signal\n", sess.Tool, sess.Project)
				}
				_ = st.SetBytesProcessed(f, info.Size())
				continue
			}

			if dryRun {
				fmt.Printf("  would distill: [%s] %s — %d turns (%d user), %dKB, signals: %s\n",
					sess.Tool, sess.Project, len(sess.Turns), sess.UserTurns(), sess.TextSize()/1024, strings.Join(kinds, "+"))
				distilled++
				continue
			}

			fmt.Printf("  distilling [%s] %s (%d turns)…\n", sess.Tool, sess.Project, len(sess.Turns))
			suggestions, err := d.Session(ctx, sess)
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				fmt.Fprintf(os.Stderr, "  distill failed for %s: %v\n", sess.Project, err)
				consecutiveFailures++
				if consecutiveFailures >= 3 {
					return fmt.Errorf("aborting scan after %d consecutive LLM failures (last: %v)", consecutiveFailures, err)
				}
				continue // LLM errors are transient: do not advance the mark; retry next scan
			}
			consecutiveFailures = 0
			distilled++

			var keep []store.Suggestion
			for _, g := range suggestions {
				exists, err := st.TitleExists(g.RepoRoot, g.Title)
				if err != nil {
					return err
				}
				if exists {
					continue
				}
				// near-dupe guard beneath the exact-title DB check: catches rephrasings that share
				// the same normalized title+body but differ in punctuation/casing.
				fp := fingerprint(g.RepoRoot, g.Title, g.Body)
				if fingerprints.Contains(fp) {
					continue
				}
				fingerprints.Add(fp, true)
				// Every suggestion lands pending. Scanning proposes; only a human accepting in
				// `autoskills review` writes a file.
				keep = append(keep, g)
			}
			// The high-water mark is the claim "everything up to here is stored". It advances in
			// the same transaction as the suggestions it covers, so a failed insert cannot leave
			// this file's findings unrecoverable behind an advanced mark.
			if err := st.AdvanceCheckpoint(f, info.Size(), keep); err != nil {
				return err
			}
			stored += len(keep)
			for _, g := range keep {
				fmt.Printf("    + %s (%s, %.0f%%)\n", g.Title, g.Signal, g.Confidence*100)
			}
		}
	}

	if dryRun {
		fmt.Printf("\ndry run: %d sessions would be distilled\n", distilled)
		return nil
	}
	fmt.Printf("\n%d sessions distilled, %d suggestions stored", distilled, stored)
	if stored > 0 {
		fmt.Print(" — run autoskills review")
	}
	fmt.Println()
	return nil
}

// daemonDebounce is how long a transcript dir must be quiet before we distill it. A session file
// is appended to continuously while a chat is live; waiting for it to settle avoids re-distilling
// the same growing transcript on every keystroke-flush.
const daemonDebounce = 3 * time.Second

// cmdDaemon runs the always-on loop. Preferred path: an fsnotify watcher reacts the instant a
// transcript dir settles (debounced), so new sessions are distilled within seconds at near-zero
// idle cost. A full sweep every DaemonIntervalMinutes is the safety net. If fsnotify can't be set
// up (unsupported FS, network mount, descriptor limits, WSL), we fall back to a 2-minute
// newest-mtime poll — belt and suspenders, never a hard dependency on file events.
func cmdDaemon(args []string) error {
	fs := flag.NewFlagSet("daemon", flag.ExitOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	scan := func() {
		if err := runScan(ctx, cfg, st, scanOptions{since: 720 * time.Hour, maxSessions: 20}); err != nil && ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "daemon scan: %v\n", err)
		}
	}
	scan() // catch up on start

	adapterList, roots := adapters()
	watcher, watched := newTranscriptWatcher(roots)
	if watcher == nil {
		fmt.Printf("autoskills daemon — file events unavailable, polling every 2m (full sweep every %dm)\n", cfg.DaemonIntervalMinutes)
		return daemonPoll(ctx, scan, cfg, adapterList)
	}
	defer watcher.Close()
	fmt.Printf("autoskills daemon — watching %d dirs, full sweep every %dm (ctrl-c to stop)\n", watched, cfg.DaemonIntervalMinutes)
	return daemonWatch(ctx, scan, cfg, watcher)
}

// newTranscriptWatcher builds an fsnotify watcher over every transcript root and its existing
// subdirectories (fsnotify is not recursive). Returns (nil, 0) if events are unavailable so the
// caller can fall back to polling. The count is how many dirs are being watched.
func newTranscriptWatcher(roots map[string]string) (*fsnotify.Watcher, int) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, 0
	}
	added := 0
	for _, root := range roots {
		if root == "" {
			continue
		}
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || !d.IsDir() {
				return nil
			}
			if w.Add(path) == nil {
				added++
			}
			return nil
		})
	}
	if added == 0 {
		w.Close()
		return nil, 0
	}
	return w, added
}

// daemonWatch is the event-driven loop: debounce filesystem events, scan once the dust settles,
// and add newly created project dirs to the watch set.
func daemonWatch(ctx context.Context, scan func(), cfg config.Config, w *fsnotify.Watcher) error {
	sweep := time.NewTicker(time.Duration(cfg.DaemonIntervalMinutes) * time.Minute)
	defer sweep.Stop()

	var debounceTimer *time.Timer
	var debounceC <-chan time.Time
	schedule := func() {
		if debounceTimer != nil {
			debounceTimer.Stop()
		}
		debounceTimer = time.NewTimer(daemonDebounce)
		debounceC = debounceTimer.C
	}

	for {
		select {
		case <-ctx.Done():
			fmt.Println("daemon: shutting down")
			return nil
		case ev, ok := <-w.Events:
			if !ok {
				return nil
			}
			// A newly created project directory must join the watch set or its sessions are missed.
			if ev.Op&fsnotify.Create != 0 {
				if info, err := os.Stat(ev.Name); err == nil && info.IsDir() {
					_ = w.Add(ev.Name)
				}
			}
			if isTranscriptEvent(ev) {
				schedule()
			}
		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			fmt.Fprintf(os.Stderr, "daemon watch: %v\n", err)
		case <-debounceC:
			debounceC = nil
			scan()
		case <-sweep.C:
			scan()
		}
	}
}

// isTranscriptEvent keeps only writes/creates to transcript files (.jsonl), the content-bearing
// events; renames/removes and noise files don't trigger a distill.
func isTranscriptEvent(ev fsnotify.Event) bool {
	if ev.Op&(fsnotify.Write|fsnotify.Create) == 0 {
		return false
	}
	return strings.HasSuffix(ev.Name, ".jsonl")
}

// daemonPoll is the fallback loop when file events are unavailable: a cheap newest-mtime probe
// every 2 minutes triggers a scan when fresh transcript bytes exist; a full sweep is the net.
func daemonPoll(ctx context.Context, scan func(), cfg config.Config, adapterList []collector.Adapter) error {
	probe := time.NewTicker(2 * time.Minute)
	sweep := time.NewTicker(time.Duration(cfg.DaemonIntervalMinutes) * time.Minute)
	defer probe.Stop()
	defer sweep.Stop()
	lastSeen := time.Now()
	for {
		select {
		case <-ctx.Done():
			fmt.Println("daemon: shutting down")
			return nil
		case <-probe.C:
			if newestTranscriptMtime(adapterList).After(lastSeen) {
				lastSeen = time.Now()
				scan()
			}
		case <-sweep.C:
			lastSeen = time.Now()
			scan()
		}
	}
}

func newestTranscriptMtime(adapterList []collector.Adapter) time.Time {
	var newest time.Time
	for _, a := range adapterList {
		files, err := a.SessionFiles() // sorted newest-first
		if err != nil || len(files) == 0 {
			continue
		}
		if st, err := os.Stat(files[0]); err == nil && st.ModTime().After(newest) {
			newest = st.ModTime()
		}
	}
	return newest
}

const launchdLabel = "io.autoskills.daemon"

func cmdInstallDaemon(args []string) error {
	fs := flag.NewFlagSet("install-daemon", flag.ExitOnError)
	uninstall := fs.Bool("uninstall", false, "remove the background service")
	if err := fs.Parse(args); err != nil {
		return err
	}
	switch runtime.GOOS {
	case "darwin":
		return installLaunchd(*uninstall)
	case "linux":
		return installSystemd(*uninstall)
	default:
		return fmt.Errorf("install-daemon supports macOS (launchd) and Linux (systemd --user); on Windows run `autoskills daemon` manually or via Task Scheduler (native service support is on the roadmap)")
	}
}

// installSystemd manages a systemd user unit so the daemon runs at login without root.
func installSystemd(uninstall bool) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	unitDir := filepath.Join(home, ".config", "systemd", "user")
	unitPath := filepath.Join(unitDir, "autoskills.service")

	if uninstall {
		_ = exec.Command("systemctl", "--user", "disable", "--now", "autoskills.service").Run()
		if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
			return err
		}
		fmt.Println("daemon uninstalled")
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	unit := fmt.Sprintf(`[Unit]
Description=autoskills daemon — distill agent sessions into skills

[Service]
ExecStart=%s daemon
Restart=on-failure
Nice=10

[Install]
WantedBy=default.target
`, exe)
	if err := os.MkdirAll(unitDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(unitPath, []byte(unit), 0o644); err != nil {
		return err
	}
	if out, err := exec.Command("systemctl", "--user", "daemon-reload").CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %v: %s", err, out)
	}
	if out, err := exec.Command("systemctl", "--user", "enable", "--now", "autoskills.service").CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl enable: %v: %s", err, out)
	}
	fmt.Printf("daemon installed (%s)\nlogs: journalctl --user -u autoskills.service\n", unitPath)
	return nil
}

func installLaunchd(uninstall bool) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	plistPath := home + "/Library/LaunchAgents/" + launchdLabel + ".plist"

	if uninstall {
		_ = exec.Command("launchctl", "unload", plistPath).Run()
		if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
			return err
		}
		fmt.Println("daemon uninstalled")
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key>
  <array><string>%s</string><string>daemon</string></array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ProcessType</key><string>Background</string>
  <key>StandardOutPath</key><string>%s/.autoskills/daemon.log</string>
  <key>StandardErrorPath</key><string>%s/.autoskills/daemon.log</string>
</dict>
</plist>
`, launchdLabel, exe, home, home)
	if err := os.MkdirAll(home+"/.autoskills", 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(plistPath, []byte(plist), 0o644); err != nil {
		return err
	}
	_ = exec.Command("launchctl", "unload", plistPath).Run() // reload if already installed
	if out, err := exec.Command("launchctl", "load", plistPath).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl load: %v: %s", err, out)
	}
	fmt.Printf("daemon installed (%s)\nlogs: ~/.autoskills/daemon.log\n", plistPath)
	return nil
}

func cmdGarden(args []string) error {
	fs := flag.NewFlagSet("garden", flag.ExitOnError)
	repo := fs.String("repo", ".", "repo to garden (default: current directory)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	repoRoot, err := filepath.Abs(*repo)
	if err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	writer.SectionBudgetBytes = cfg.SectionBudgetBytes

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	provider, err := configuredProvider(cfg)
	if err != nil {
		return err
	}
	d := &distill.Distiller{Provider: provider, Store: st, MinConfidence: cfg.MinConfidence}
	suggestions, err := d.Garden(ctx, repoRoot, filepath.Base(repoRoot))
	if err != nil {
		return err
	}
	stored := 0
	for _, g := range suggestions {
		// repeated garden runs must not stack duplicate amend/prune items in the inbox
		exists, err := st.TitleExists(g.RepoRoot, g.Title)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		if err := st.InsertSuggestion(g); err != nil {
			return err
		}
		stored++
		fmt.Printf("  + %s (%.0f%%) — %s\n", g.Title, g.Confidence*100, g.Rationale)
	}
	if stored == 0 {
		fmt.Println("garden: nothing to improve — the skill section is healthy")
	} else {
		fmt.Printf("\n%d gardening actions proposed — run autoskills review\n", stored)
	}
	return nil
}

// cmdVerify is the deterministic staleness pass: skills referencing paths that no longer
// exist in the repo are flagged for gardening. No LLM, no network.
func cmdVerify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	repo := fs.String("repo", ".", "repo to verify (default: current directory)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	repoRoot, err := filepath.Abs(*repo)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(filepath.Join(repoRoot, "AGENTS.md"))
	if err != nil {
		return fmt.Errorf("no AGENTS.md in %s", repoRoot)
	}
	blocks := writer.ParseBlocks(string(raw))
	if len(blocks) == 0 {
		fmt.Println("verify: no managed skills found")
		return nil
	}

	// changed-set is best-effort provenance: paths touched since the previous commit. A skill that
	// still references an existing-but-recently-rewritten file may have drifted, so we surface it
	// as a softer signal than an outright missing path. Only meaningful when a parent commit
	// exists — on a fresh single-commit repo there is no prior state to have drifted from, and
	// diffing against the empty tree would mark every tracked file "changed".
	changed := map[string]bool{}
	if gitmeta.HasParentCommit(repoRoot) {
		for _, f := range gitmeta.ChangedFiles(repoRoot) {
			changed[f] = true
		}
	}

	stale, drifted := 0, 0
	for _, b := range blocks {
		var missing, touched []string
		for _, ref := range pathRefs(b.Body) {
			if _, err := os.Stat(filepath.Join(repoRoot, ref)); os.IsNotExist(err) {
				missing = append(missing, ref)
			} else if changed[ref] {
				touched = append(touched, ref)
			}
		}
		if len(missing) > 0 {
			stale++
			fmt.Printf("STALE  %s — %s\n       missing: %s\n", b.ID, writer.BlockTitle(b.Body), strings.Join(missing, ", "))
		} else if len(touched) > 0 {
			drifted++
			fmt.Printf("CHANGED %s — %s\n        modified in latest commit: %s\n", b.ID, writer.BlockTitle(b.Body), strings.Join(touched, ", "))
		}
	}
	switch {
	case stale == 0 && drifted == 0:
		fmt.Printf("verify: all %d skills reference existing paths\n", len(blocks))
	default:
		fmt.Printf("\n%d stale, %d possibly-drifted skill(s) — run autoskills garden to propose fixes\n", stale, drifted)
	}
	return nil
}

var backtickRe = regexp.MustCompile("`([^`\n]+)`")

// pathRefs extracts backticked tokens that look like repo-relative paths.
func pathRefs(body string) []string {
	var out []string
	for _, m := range backtickRe.FindAllStringSubmatch(body, -1) {
		t := strings.TrimSpace(m[1])
		// repo-relative path heuristic: has a separator, no spaces/flags/absolute/home/glob
		if !strings.Contains(t, "/") || strings.ContainsAny(t, " *$~") || strings.HasPrefix(t, "/") || strings.HasPrefix(t, "-") || strings.Contains(t, "://") {
			continue
		}
		out = append(out, t)
	}
	return out
}

func cmdUndo(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: autoskills undo <suggestion-id>")
	}
	id := args[0]
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	writer.SectionBudgetBytes = cfg.SectionBudgetBytes
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	g, err := st.GetSuggestion(id)
	if err != nil {
		return fmt.Errorf("suggestion %s not found", id)
	}
	if g.Status != "accepted" {
		return fmt.Errorf("suggestion %s is %s, not accepted", id, g.Status)
	}
	// A gardener action rewrites existing block content in place. Undoing it is a restoration, not
	// a deletion — so it is available exactly when the acceptance recorded what it overwrote. A
	// journaled acceptance carries that manifest and can be put back byte for byte; one accepted
	// before the journal existed carries nothing, and recomputing a removal there would delete a
	// block instead of restoring the one it replaced. That case is refused by name.
	if g.Tool == "gardener" {
		journaled, err := writer.HasJournaledAcceptance(st, g.ID)
		if err != nil {
			return err
		}
		if !journaled {
			return fmt.Errorf("cannot undo gardener action %s: it was accepted before the acceptance journal existed, so the block content it replaced was never recorded and cannot be restored; recover it from the git history of %s", id, g.WrittenPath)
		}
	}
	// journaled like an accept: the artifact removal and the return to pending cannot come apart
	if err := writer.Undo(st, g); err != nil {
		return fmt.Errorf("remove artifact: %w", err)
	}
	fmt.Printf("undone: %s — artifact removed, suggestion back in the inbox\n", g.Title)
	return nil
}

// worthDistilling filters out sessions too small to contain skill-shaped signal.
func worthDistilling(s *canon.Session) bool {
	return s.UserTurns() >= 2 && s.TextSize() >= 1500
}

// fingerprint normalizes a suggestion to a dedupe key: repo + lowercased/whitespace-collapsed
// title + the first 80 chars of the same normalization of the body.
func fingerprint(repoRoot, title, body string) string {
	norm := func(s string) string { return strings.Join(strings.Fields(strings.ToLower(s)), " ") }
	b := norm(body)
	if len(b) > 80 {
		b = b[:80]
	}
	return repoRoot + "|" + norm(title) + "|" + b
}

// ignored enforces the config ignore_projects privacy control: entries match a project name
// (case-insensitive) or a repo-root path prefix.
func ignored(list []string, project, repoRoot string) bool {
	for _, item := range list {
		if item == "" {
			continue
		}
		if strings.EqualFold(item, project) {
			return true
		}
		if repoRoot != "" && strings.HasPrefix(repoRoot, item) {
			return true
		}
	}
	return false
}

func cmdReview(args []string) error {
	fs := flag.NewFlagSet("review", flag.ExitOnError)
	addr := fs.String("addr", server.DefaultAddr, "listen address")
	noOpen := fs.Bool("no-open", false, "do not open the browser")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	writer.SectionBudgetBytes = cfg.SectionBudgetBytes // dashboard accepts respect the configured budget

	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	// the listen address is passed in, not assumed: it decides which Host a browser may present,
	// and a wildcard address grants no name beyond loopback
	srv := &server.Server{Store: st, Addr: *addr}
	url := "http://" + *addr
	fmt.Printf("autoskills review — %s (ctrl-c to stop)\n", url)
	if !*noOpen {
		go func() {
			time.Sleep(300 * time.Millisecond)
			openBrowser(url)
		}()
	}
	return http.ListenAndServe(*addr, srv.Handler())
}

func openBrowser(url string) {
	switch runtime.GOOS {
	case "darwin":
		_ = exec.Command("open", url).Start()
	case "linux":
		_ = exec.Command("xdg-open", url).Start()
	case "windows":
		_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	}
}

func cmdStatus(args []string) error {
	st, err := store.Open(store.DefaultPath())
	if err != nil {
		return err
	}
	defer st.Close()

	adapterList, roots := adapters()
	for _, e := range collector.Discover(adapterList, roots) {
		fmt.Printf("%-8s %4d sessions   %s\n", e.Tool, e.Sessions, e.Root)
	}
	stats, err := st.Stats()
	if err != nil {
		return err
	}
	fmt.Printf("\nsuggestions: %d pending · %d accepted · %d rejected (%d sessions, %d projects)\n",
		stats.Pending, stats.Accepted, stats.Rejected, stats.Sessions, stats.Projects)
	// status reports, it never mutates: interrupted operations are named here and reconciled by
	// the next command that actually opens the store for work.
	if open, err := st.IncompleteOperations(); err == nil && len(open) > 0 {
		fmt.Printf("\n%d interrupted operation(s) pending reconciliation — the next scan, review or undo completes or restores them:\n", len(open))
		for _, op := range open {
			fmt.Printf("  %s  %s %s (%s)\n", op.ID, op.Kind, op.SuggestionID, op.State)
		}
	}
	return nil
}
