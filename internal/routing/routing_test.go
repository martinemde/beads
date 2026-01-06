package routing

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDetermineTargetRepo(t *testing.T) {
	tests := []struct {
		name     string
		config   *RoutingConfig
		userRole UserRole
		repoPath string
		want     string
	}{
		{
			name: "explicit override takes precedence",
			config: &RoutingConfig{
				Mode:             "auto",
				DefaultRepo:      "~/planning",
				MaintainerRepo:   ".",
				ContributorRepo:  "~/contributor-planning",
				ExplicitOverride: "/tmp/custom",
			},
			userRole: Maintainer,
			repoPath: ".",
			want:     "/tmp/custom",
		},
		{
			name: "auto mode - maintainer uses maintainer repo",
			config: &RoutingConfig{
				Mode:            "auto",
				MaintainerRepo:  ".",
				ContributorRepo: "~/contributor-planning",
			},
			userRole: Maintainer,
			repoPath: ".",
			want:     ".",
		},
		{
			name: "auto mode - contributor uses contributor repo",
			config: &RoutingConfig{
				Mode:            "auto",
				MaintainerRepo:  ".",
				ContributorRepo: "~/contributor-planning",
			},
			userRole: Contributor,
			repoPath: ".",
			want:     "~/contributor-planning",
		},
		{
			name: "explicit mode uses default",
			config: &RoutingConfig{
				Mode:        "explicit",
				DefaultRepo: "~/planning",
			},
			userRole: Maintainer,
			repoPath: ".",
			want:     "~/planning",
		},
		{
			name: "no config defaults to current directory",
			config: &RoutingConfig{
				Mode: "auto",
			},
			userRole: Maintainer,
			repoPath: ".",
			want:     ".",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetermineTargetRepo(tt.config, tt.userRole, tt.repoPath)
			if got != tt.want {
				t.Errorf("DetermineTargetRepo() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDetectUserRole_Fallback(t *testing.T) {
	// Test fallback behavior when git is not available
	role, err := DetectUserRole("/nonexistent/path/that/does/not/exist")
	if err != nil {
		t.Fatalf("DetectUserRole() error = %v, want nil", err)
	}
	if role != Contributor {
		t.Errorf("DetectUserRole() = %v, want %v (fallback)", role, Contributor)
	}
}

func TestExtractPrefix(t *testing.T) {
	tests := []struct {
		id   string
		want string
	}{
		{"gt-abc123", "gt-"},
		{"bd-xyz", "bd-"},
		{"hq-1234", "hq-"},
		{"abc123", ""}, // No hyphen
		{"", ""},       // Empty string
		{"-abc", "-"},  // Starts with hyphen
	}

	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			got := ExtractPrefix(tt.id)
			if got != tt.want {
				t.Errorf("ExtractPrefix(%q) = %q, want %q", tt.id, got, tt.want)
			}
		})
	}
}

func TestExtractProjectFromPath(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"beads/mayor/rig", "beads"},
		{"gastown/crew/max", "gastown"},
		{"simple", "simple"},
		{"", ""},
		{"/absolute/path", ""}, // Starts with /, first component is empty
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := ExtractProjectFromPath(tt.path)
			if got != tt.want {
				t.Errorf("ExtractProjectFromPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestResolveToExternalRef(t *testing.T) {
	// This test is limited since it requires a routes.jsonl file
	// Just test that it returns empty string for nonexistent directory
	got := ResolveToExternalRef("bd-abc", "/nonexistent/path")
	if got != "" {
		t.Errorf("ResolveToExternalRef() = %q, want empty string for nonexistent path", got)
	}
}

type gitCall struct {
	repo string
	args []string
}

type gitResponse struct {
	expect gitCall
	output string
	err    error
}

type gitStub struct {
	t         *testing.T
	responses []gitResponse
	idx       int
}

func (s *gitStub) run(repo string, args ...string) ([]byte, error) {
	if s.idx >= len(s.responses) {
		s.t.Fatalf("unexpected git call %v in repo %s", args, repo)
	}
	resp := s.responses[s.idx]
	s.idx++
	if resp.expect.repo != repo {
		s.t.Fatalf("repo mismatch: got %q want %q", repo, resp.expect.repo)
	}
	if !reflect.DeepEqual(resp.expect.args, args) {
		s.t.Fatalf("args mismatch: got %v want %v", args, resp.expect.args)
	}
	return []byte(resp.output), resp.err
}

func (s *gitStub) verify() {
	if s.idx != len(s.responses) {
		s.t.Fatalf("expected %d git calls, got %d", len(s.responses), s.idx)
	}
}

func TestDetectUserRole_ConfigOverrideMaintainer(t *testing.T) {
	orig := gitCommandRunner
	stub := &gitStub{t: t, responses: []gitResponse{
		{expect: gitCall{"", []string{"config", "--get", "beads.role"}}, output: "maintainer\n"},
	}}
	gitCommandRunner = stub.run
	t.Cleanup(func() {
		gitCommandRunner = orig
		stub.verify()
	})

	role, err := DetectUserRole("")
	if err != nil {
		t.Fatalf("DetectUserRole error = %v", err)
	}
	if role != Maintainer {
		t.Fatalf("expected %s, got %s", Maintainer, role)
	}
}

func TestDetectUserRole_ConfigOverrideContributor(t *testing.T) {
	orig := gitCommandRunner
	stub := &gitStub{t: t, responses: []gitResponse{
		{expect: gitCall{"/repo", []string{"config", "--get", "beads.role"}}, output: "contributor\n"},
	}}
	gitCommandRunner = stub.run
	t.Cleanup(func() {
		gitCommandRunner = orig
		stub.verify()
	})

	role, err := DetectUserRole("/repo")
	if err != nil {
		t.Fatalf("DetectUserRole error = %v", err)
	}
	if role != Contributor {
		t.Fatalf("expected %s, got %s", Contributor, role)
	}
}

func TestDetectUserRole_PushURLMaintainer(t *testing.T) {
	orig := gitCommandRunner
	stub := &gitStub{t: t, responses: []gitResponse{
		{expect: gitCall{"/repo", []string{"config", "--get", "beads.role"}}, output: "unknown"},
		{expect: gitCall{"/repo", []string{"remote", "get-url", "--push", "origin"}}, output: "git@github.com:owner/repo.git"},
	}}
	gitCommandRunner = stub.run
	t.Cleanup(func() {
		gitCommandRunner = orig
		stub.verify()
	})

	role, err := DetectUserRole("/repo")
	if err != nil {
		t.Fatalf("DetectUserRole error = %v", err)
	}
	if role != Maintainer {
		t.Fatalf("expected %s, got %s", Maintainer, role)
	}
}

func TestDetectUserRole_HTTPSCredentialsMaintainer(t *testing.T) {
	orig := gitCommandRunner
	stub := &gitStub{t: t, responses: []gitResponse{
		{expect: gitCall{"/repo", []string{"config", "--get", "beads.role"}}, output: ""},
		{expect: gitCall{"/repo", []string{"remote", "get-url", "--push", "origin"}}, output: "https://token@github.com/owner/repo.git"},
	}}
	gitCommandRunner = stub.run
	t.Cleanup(func() {
		gitCommandRunner = orig
		stub.verify()
	})

	role, err := DetectUserRole("/repo")
	if err != nil {
		t.Fatalf("DetectUserRole error = %v", err)
	}
	if role != Maintainer {
		t.Fatalf("expected %s, got %s", Maintainer, role)
	}
}

func TestDetectUserRole_DefaultContributor(t *testing.T) {
	orig := gitCommandRunner
	stub := &gitStub{t: t, responses: []gitResponse{
		{expect: gitCall{"", []string{"config", "--get", "beads.role"}}, err: errors.New("missing")},
		{expect: gitCall{"", []string{"remote", "get-url", "--push", "origin"}}, err: errors.New("no push")},
		{expect: gitCall{"", []string{"remote", "get-url", "origin"}}, output: "https://github.com/owner/repo.git"},
	}}
	gitCommandRunner = stub.run
	t.Cleanup(func() {
		gitCommandRunner = orig
		stub.verify()
	})

	role, err := DetectUserRole("")
	if err != nil {
		t.Fatalf("DetectUserRole error = %v", err)
	}
	if role != Contributor {
		t.Fatalf("expected %s, got %s", Contributor, role)
	}
}

func TestFindTownRoutes_RigWithLocalRoutes(t *testing.T) {
	// Create a temp directory structure simulating a town with a rig
	// that has its own routes.jsonl file
	tmpDir := t.TempDir()

	// Create town structure: mayor/town.json marks the town root
	townRoot := tmpDir
	if err := os.MkdirAll(filepath.Join(townRoot, "mayor"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(townRoot, "mayor", "town.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create town-level beads with routes
	townBeadsDir := filepath.Join(townRoot, ".beads")
	if err := os.MkdirAll(townBeadsDir, 0755); err != nil {
		t.Fatal(err)
	}
	townRoutes := `{"prefix": "hq-", "path": "."}
{"prefix": "bd-", "path": "beads/mayor/rig"}
`
	if err := os.WriteFile(filepath.Join(townBeadsDir, "routes.jsonl"), []byte(townRoutes), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a rig inside the town with its own routes.jsonl
	rigBeadsDir := filepath.Join(townRoot, "myrig", ".beads")
	if err := os.MkdirAll(rigBeadsDir, 0755); err != nil {
		t.Fatal(err)
	}
	// The rig has a different/stale routes file - this should NOT be used
	rigRoutes := `{"prefix": "rig-", "path": "stale/path"}
`
	if err := os.WriteFile(filepath.Join(rigBeadsDir, "routes.jsonl"), []byte(rigRoutes), 0644); err != nil {
		t.Fatal(err)
	}

	// Test: from rig beads dir, findTownRoutes should return town routes and town root
	routes, gotTownRoot := findTownRoutes(rigBeadsDir)

	// Should return routes from town, not from rig
	if len(routes) != 2 {
		t.Errorf("expected 2 routes from town, got %d", len(routes))
	}

	// Should return the actual town root, not the rig directory
	if gotTownRoot != townRoot {
		t.Errorf("expected townRoot=%q, got %q", townRoot, gotTownRoot)
	}

	// Verify we got town routes (hq-, bd-), not rig routes (rig-)
	foundHQ := false
	foundBD := false
	for _, r := range routes {
		if r.Prefix == "hq-" {
			foundHQ = true
		}
		if r.Prefix == "bd-" {
			foundBD = true
		}
		if r.Prefix == "rig-" {
			t.Errorf("unexpected rig route found - should use town routes, not rig-local routes")
		}
	}
	if !foundHQ || !foundBD {
		t.Errorf("expected town routes (hq-, bd-), got: %v", routes)
	}
}

func TestFindTownRoutes_StandaloneBeads(t *testing.T) {
	// Test that standalone beads (not in a town) still work with local routes
	tmpDir := t.TempDir()

	// Create a beads directory with routes but no town structure
	beadsDir := filepath.Join(tmpDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0755); err != nil {
		t.Fatal(err)
	}
	localRoutes := `{"prefix": "local-", "path": "somewhere"}
`
	if err := os.WriteFile(filepath.Join(beadsDir, "routes.jsonl"), []byte(localRoutes), 0644); err != nil {
		t.Fatal(err)
	}

	// Test: findTownRoutes should fall back to local routes
	routes, gotRoot := findTownRoutes(beadsDir)

	if len(routes) != 1 {
		t.Errorf("expected 1 local route, got %d", len(routes))
	}

	if routes[0].Prefix != "local-" {
		t.Errorf("expected local- prefix, got %s", routes[0].Prefix)
	}

	// Root should be parent of beads dir for standalone usage
	expectedRoot := tmpDir
	if gotRoot != expectedRoot {
		t.Errorf("expected root=%q, got %q", expectedRoot, gotRoot)
	}
}
