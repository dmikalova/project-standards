package checks

import (
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestParseCommitRange(t *testing.T) {
	const zero = "0000000000000000000000000000000000000000"
	tests := []struct {
		in, from, to string
		wantErr      bool
	}{
		{in: "abc123..def456", from: "abc123", to: "def456"},
		{in: "  abc..def\n", from: "abc", to: "def"},
		{in: "origin/main..HEAD", from: "origin/main", to: "HEAD"},
		{in: zero + "..def456", from: "", to: "def456"},
		{in: strings.Repeat("0", 64) + "..def", from: "", to: "def"}, // SHA-256 repositories
		{in: "0..def", from: "", to: "def"},
		{in: "0a0..def", from: "0a0", to: "def"},
		{in: "abc", wantErr: true},
		{in: "abc..", wantErr: true},
		{in: "..def", wantErr: true},
		{in: "..", wantErr: true},
		{in: "abc...def", wantErr: true},
		{in: "a..b..c", wantErr: true},
	}
	for _, tt := range tests {
		from, to, err := parseCommitRange(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("parseCommitRange(%q) = %q, %q; want an error", tt.in, from, to)
			}
			continue
		}
		if err != nil || from != tt.from || to != tt.to {
			t.Errorf("parseCommitRange(%q) = %q, %q, %v; want %q, %q", tt.in, from, to, err,
				tt.from, tt.to)
		}
	}
}

// TestCommitRange resolves ranges in a real repository: main with one commit,
// and a feature branch with two more.
func TestCommitRange(t *testing.T) {
	g := newRepo(t)
	g("commit", "-q", "--allow-empty", "-m", "feat: base")
	mainSHA := g("rev-parse", "HEAD")
	g("checkout", "-q", "-b", "feature")
	g("commit", "-q", "--allow-empty", "-m", "feat: one")
	oneSHA := g("rev-parse", "HEAD")
	g("commit", "-q", "--allow-empty", "-m", "feat: two")
	twoSHA := g("rev-parse", "HEAD")

	tests := []struct {
		name, env, want string
		wantErr         bool
	}{
		{name: "unset lints since the merge-base", env: "", want: mainSHA + "..HEAD"},
		{
			name: "explicit range is used as is",
			env:  oneSHA + ".." + twoSHA,
			want: oneSHA + ".." + twoSHA,
		},
		{
			name: "zero from lints from the merge-base",
			env:  strings.Repeat("0", 40) + ".." + twoSHA,
			want: mainSHA + ".." + twoSHA,
		},
		{name: "malformed range is an error", env: "nonsense", wantErr: true},
		{name: "zero from with unknown to is an error", env: "0000..nosuchref", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := testChecks.commitRange(tt.env)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("commitRange(%q) = %q; want an error", tt.env, got)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("commitRange(%q) = %q, %v; want %q", tt.env, got, err, tt.want)
			}
		})
	}
}

func TestCommitRangeWithoutDefaultBranch(t *testing.T) {
	g := newRepo(t)
	g("checkout", "-q", "-b", "trunk")
	// Unset, no default branch is a clean skip.
	var got string
	var err error
	out := captureStdout(t, func() { got, err = testChecks.commitRange("") })
	if err != nil || got != "" || !strings.Contains(out, "test commits: skipped: no default") {
		t.Errorf(`commitRange("") = %q, %v, printing %q; want a skip`, got, err, out)
	}
	// Set with a zero from, the missing default branch is an error.
	if _, err := testChecks.commitRange("0000..HEAD"); err == nil {
		t.Error("commitRange with a zero from and no default branch succeeded")
	}
}

func TestCommitRangeWithoutMergeBase(t *testing.T) {
	g := newRepo(t)
	g("commit", "-q", "--allow-empty", "-m", "feat: base")
	g("checkout", "-q", "--orphan", "other")
	g("commit", "-q", "--allow-empty", "-m", "feat: unrelated")
	var got string
	var err error
	out := captureStdout(t, func() { got, err = testChecks.commitRange("") })
	if err != nil || got != "" || !strings.Contains(out, "no merge-base between main and HEAD") {
		t.Errorf(`commitRange("") = %q, %v, printing %q; want a skip`, got, err, out)
	}
}

func TestDefaultBranchFromRemoteHead(t *testing.T) {
	g := newRepo(t)
	g("commit", "-q", "--allow-empty", "-m", "feat: base")
	g("update-ref", "refs/remotes/origin/trunk", "HEAD")
	g("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/trunk")
	if got, err := defaultBranch(); err != nil || got != "origin/trunk" {
		t.Errorf("defaultBranch() = %q, %v; want origin/trunk", got, err)
	}
}

func TestCommits(t *testing.T) {
	g := newRepo(t)
	g("commit", "-q", "--allow-empty", "-m", "feat: base")
	g("checkout", "-q", "-b", "feature")
	g("commit", "-q", "--allow-empty", "-m", "feat: good\n\nA body.")
	g("commit", "-q", "--allow-empty", "-m", "bad message")
	bad := g("rev-parse", "--short=12", "HEAD")
	calls := fakeGo(t, func(c goCall, stdout, _ io.Writer) int {
		if strings.HasPrefix(c.stdin, "bad") {
			_, _ = io.WriteString(stdout, "type is missing\n")
			return 1
		}
		return 0
	})
	var err error
	out := captureStdout(t, func() { err = testChecks.Commits() })
	wantErr(t, err, "commit messages failed the commit lint: "+bad)
	if !strings.Contains(out, bad+" bad message\ntype is missing\n") ||
		!strings.Contains(out, "test commits: linted 2 commit(s)") {
		t.Errorf("output = %q", out)
	}
	var stdins []string
	for _, c := range *calls {
		if !reflect.DeepEqual(c.args, []string{"commitlint@v0", "lint"}) {
			t.Errorf("commitlint args = %v, want no --config without %s", c.args, commitlintConfig)
		}
		stdins = append(stdins, c.stdin)
	}
	if want := []string{"feat: good\n\nA body.\n", "bad message\n"}; !reflect.DeepEqual(
		stdins, want) {
		t.Errorf("messages = %q, want %q", stdins, want)
	}

	// With the generated config present, commitlint is pointed at it.
	writeFiles(t, map[string]string{commitlintConfig: "rules: []\n"})
	calls = passGo(t)
	t.Setenv(CommitRangeEnv, "HEAD~1..HEAD")
	if err := testChecks.Commits(); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || !reflect.DeepEqual((*calls)[0].args,
		[]string{"commitlint@v0", "lint", "--config", commitlintConfig}) {
		t.Errorf("calls = %v, want one commitlint run with --config", *calls)
	}
}

func TestCommitsNothingToLint(t *testing.T) {
	g := newRepo(t)
	g("commit", "-q", "--allow-empty", "-m", "feat: base")
	calls := passGo(t)
	out := captureStdout(t, func() {
		if err := testChecks.Commits(); err != nil {
			t.Error(err)
		}
	})
	if len(*calls) != 0 || !strings.Contains(out, "test commits: no commits in") {
		t.Errorf("calls = %v, output %q; want nothing linted", *calls, out)
	}
	// Without a default branch the check is skipped.
	g("branch", "-q", "-m", "trunk")
	if err := testChecks.Commits(); err != nil {
		t.Error(err)
	}
}

func TestCommitsBadRange(t *testing.T) {
	newRepo(t)
	t.Setenv(CommitRangeEnv, "nonsense")
	wantErr(t, testChecks.Commits(), CommitRangeEnv+`: "nonsense": want <from>..<to>`)
	t.Setenv(CommitRangeEnv, "nosuchref..HEAD")
	wantErr(t, testChecks.Commits(), "listing commits in nosuchref..HEAD")
}

func TestCommitMessage(t *testing.T) {
	newRepo(t)
	calls := fakeGo(t, func(c goCall, stdout, _ io.Writer) int {
		if strings.HasPrefix(c.stdin, "bad") {
			_, _ = io.WriteString(stdout, "type is missing\n")
			return 1
		}
		return 0
	})
	writeFiles(t, map[string]string{
		"good": "feat: good\n\nA body.\n# Please enter the commit message.\n" +
			scissors + "\ndiff --git a/x b/x\n",
		"bad":     "bad message\n",
		"comment": "# only a comment\n\n",
	})
	if err := testChecks.CommitMessage("good"); err != nil {
		t.Errorf("CommitMessage(good) = %v", err)
	}
	if len(*calls) != 1 || (*calls)[0].stdin != "feat: good\n\nA body.\n" {
		t.Errorf("calls = %v, want the message without comments or the diff", *calls)
	}
	var err error
	out := captureStdout(t, func() { err = testChecks.CommitMessage("bad") })
	wantErr(t, err, "the commit message failed the commit lint")
	if !strings.HasPrefix(out, "type is missing\n") {
		t.Errorf("output = %q, want commitlint's", out)
	}
	wantErr(t, testChecks.CommitMessage("comment"), "the commit message is empty")
	wantErr(t, testChecks.CommitMessage("missing"), "reading the commit message")
}

func TestAgentAttribution(t *testing.T) {
	tests := map[string][]string{
		"feat: x\n\nCo-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>": {
			"Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>",
		},
		"fix: y\n\nco-authored-by: Claude <noreply@anthropic.com>": {
			"co-authored-by: Claude <noreply@anthropic.com>",
		},
		"fix: y\n\n🤖 Generated with [Claude Code](https://claude.com/claude-code)": {
			"🤖 Generated with [Claude Code](https://claude.com/claude-code)",
		},
		"feat: z\n\nCo-authored-by: Jane Doe <jane@example.com>":  nil,
		"docs: explain how Copilot adds a co-authored-by trailer": nil,
	}
	for msg, want := range tests {
		if got := agentAttribution(msg); !reflect.DeepEqual(got, want) {
			t.Errorf("agentAttribution(%q) = %q, want %q", msg, got, want)
		}
	}
}

func TestCommitMessageAgentAttribution(t *testing.T) {
	newRepo(t)
	passGo(t)
	writeFiles(t, map[string]string{
		"msg": "feat: good\n\nCo-authored-by: Claude <noreply@anthropic.com>\n",
	})
	var err error
	out := captureStdout(t, func() { err = testChecks.CommitMessage("msg") })
	wantErr(t, err, "the commit message failed the commit lint")
	if !strings.Contains(out, "agent attribution is not allowed: Co-authored-by: Claude") {
		t.Errorf("output = %q, want the attribution line", out)
	}
}
