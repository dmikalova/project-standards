package checks

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Commits lints commit messages not yet on the default branch.
// By default the range is from the merge-base with the default branch to HEAD.
// With no such commits, or no default branch to compare with (a shallow clone,
// a repository without a remote), it passes. Merge commits are skipped.
//
// CI sets CI_COMMIT_RANGE to "<from>..<to>" to lint exactly that range, such as
// the commits a push to the default branch added, which the default range
// never sees. A from of all zeros, which a forge sends for the first push of a
// new branch, lints from the merge-base of to with the default branch. When
// the variable is set, a range that cannot be resolved is an error, not a
// skip: the clone must hold both ends, so CI needs full history.
//
// Only the branch's own commits are linted, never the default branch's
// history: those were linted when they were made, and a project adopting the
// standard should not fail on commits it can no longer reword.
func (c *Checks) Commits() error {
	revRange, err := c.commitRange(os.Getenv(CommitRangeEnv))
	if err != nil {
		return err
	}
	if revRange == "" {
		return nil // Skipped; commitRange said why.
	}
	// -z ends each commit with a NUL, which a commit message cannot contain.
	log, err := git("log", "--no-merges", "--reverse", "-z", "--format=%H%n%B", revRange)
	if err != nil {
		return fmt.Errorf("listing commits in %s: %w", revRange, err)
	}
	var linted int
	var failed []string
	for entry := range strings.SplitSeq(log, "\x00") {
		sha, msg, _ := strings.Cut(entry, "\n")
		if sha == "" {
			continue
		}
		linted++
		msg = strings.TrimRight(msg, "\n")
		if out, err := c.lintMessage(msg); err != nil {
			fmt.Printf("%s %s\n%s", sha[:12], firstLine(msg), out)
			failed = append(failed, sha[:12])
		}
	}
	if linted == 0 {
		fmt.Printf("%s: no commits in %s to lint\n", c.label("commits"), revRange)
		return nil
	}
	fmt.Printf("%s: linted %d commit(s) in %s\n", c.label("commits"), linted, revRange)
	if len(failed) > 0 {
		return fmt.Errorf("commit messages failed the commit lint: %s", strings.Join(failed, ", "))
	}
	return nil
}

// CommitMessage lints the commit message in path, for a commit-msg hook: git
// passes the message file as the hook's first argument. Comment lines and
// everything below the scissors line, which git strips before committing, are
// left out.
func (c *Checks) CommitMessage(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading the commit message: %w", err)
	}
	msg := cleanMessage(string(data))
	if msg == "" {
		return errors.New("the commit message is empty")
	}
	if out, err := c.lintMessage(msg); err != nil {
		fmt.Print(out)
		return errors.New("the commit message failed the commit lint")
	}
	return nil
}

// scissors is the line git's verbose commit template puts above the diff;
// git drops it and everything below it.
const scissors = "# ------------------------ >8 ------------------------"

// cleanMessage drops what git strips from a commit message file: comment lines
// and everything from the scissors line down.
func cleanMessage(s string) string {
	var lines []string
	for line := range strings.SplitSeq(s, "\n") {
		if strings.HasPrefix(line, scissors) {
			break
		}
		if !strings.HasPrefix(line, "#") {
			lines = append(lines, line)
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// lintMessage runs commitlint on one message, with the generated config when
// the project has one, and then checks it for agent attribution. It returns
// the output of both.
func (c *Checks) lintMessage(msg string) (string, error) {
	args := []string{c.Tools.Commitlint, "lint"}
	if _, err := os.Stat(commitlintConfig); err == nil {
		args = append(args, "--config", commitlintConfig)
	}
	var out bytes.Buffer
	_, err := goRun(strings.NewReader(msg+"\n"), &out, &out, args...)
	if lines := agentAttribution(msg); len(lines) > 0 {
		for _, line := range lines {
			fmt.Fprintf(&out, "agent attribution is not allowed: %s\n", line)
		}
		err = errors.Join(err, errors.New("agent attribution"))
	}
	return out.String(), err
}

// agentPattern matches a line that credits a coding agent: a Co-authored-by
// trailer for an agent, or the "Generated with Claude Code" footer. The
// owner is the author of every commit; agents do not sign their work.
var agentPattern = regexp.MustCompile(
	`(?i)^\s*co-authored-by:.*\b(claude|copilot|anthropic|openai|codex|gemini|cursor)\b` +
		`|(?i)generated with \[?claude code`)

// agentAttribution returns the lines of msg that agentPattern matches.
func agentAttribution(msg string) []string {
	var found []string
	for line := range strings.SplitSeq(msg, "\n") {
		if agentPattern.MatchString(line) {
			found = append(found, strings.TrimSpace(line))
		}
	}
	return found
}

// commitlintConfig is the generated config commitlint lints with.
const commitlintConfig = ".commitlint.yaml"

// CommitRangeEnv names the variable CI sets to the range a push added, as
// "<from>..<to>". The unified cicd workflow sets it from
// ${{ github.event.before }}..${{ github.sha }}.
const CommitRangeEnv = "CI_COMMIT_RANGE"

// commitRange returns the git revision range Commits lints. With env set it is
// that range, a zero from resolved to the merge-base of to with the default
// branch. Without it, it is merge-base..HEAD, or "" (after saying why) when
// there is no default branch or merge-base to compare with.
func (c *Checks) commitRange(env string) (string, error) {
	if env != "" {
		from, to, err := parseCommitRange(env)
		if err != nil {
			return "", fmt.Errorf("%s: %w", CommitRangeEnv, err)
		}
		if from == "" {
			base, err := defaultBranch()
			if err != nil {
				return "", fmt.Errorf("%s=%s starts at the zero commit: %w",
					CommitRangeEnv, env, err)
			}
			if from, err = git("merge-base", base, to); err != nil {
				return "", fmt.Errorf("%s=%s: no merge-base between %s and %s",
					CommitRangeEnv, env, base, to)
			}
		}
		return from + ".." + to, nil
	}
	base, err := defaultBranch()
	if err != nil {
		fmt.Printf("%s: skipped: %v\n", c.label("commits"), err)
		return "", nil
	}
	mb, err := git("merge-base", base, "HEAD")
	if err != nil {
		fmt.Printf("%s: skipped: no merge-base between %s and HEAD\n", c.label("commits"), base)
		return "", nil
	}
	return mb + "..HEAD", nil
}

// parseCommitRange splits "<from>..<to>". A from of all zeros, git's null
// object ID, is returned as "" so the caller lints from the merge-base. The
// three-dot form is rejected: it means the symmetric difference, which would
// lint commits the push did not add.
func parseCommitRange(s string) (from, to string, err error) {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "...") {
		return "", "", fmt.Errorf("%q: want <from>..<to>, not the three-dot form", s)
	}
	from, to, ok := strings.Cut(s, "..")
	if !ok || from == "" || to == "" || strings.Contains(to, "..") {
		return "", "", fmt.Errorf("%q: want <from>..<to>", s)
	}
	if strings.Trim(from, "0") == "" {
		from = ""
	}
	return from, to, nil
}

// defaultBranch returns the ref of the default branch to compare HEAD with:
// the remote's HEAD when it is known, otherwise the first of origin/main,
// origin/master, main and master that exists.
func defaultBranch() (string, error) {
	ref, err := git("symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD")
	if err == nil && ref != "" {
		return ref, nil
	}
	for _, ref := range []string{"origin/main", "origin/master", "main", "master"} {
		if _, err := git("rev-parse", "--verify", "--quiet", ref+"^{commit}"); err == nil {
			return ref, nil
		}
	}
	return "", errors.New("no default branch found (origin/HEAD, main or master)")
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
