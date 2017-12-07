package git

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRemoteURL(t *testing.T) {
	dir, err := parseRemoteURL("git://github.com/user/repo.git")
	require.NoError(t, err)
	assert.NotEmpty(t, dir)
	assert.Equal(t, gitRepo{"git://github.com/user/repo.git", "master", ""}, dir)

	dir, err = parseRemoteURL("git://github.com/user/repo.git#mybranch:mydir/mysubdir/")
	require.NoError(t, err)
	assert.NotEmpty(t, dir)
	assert.Equal(t, gitRepo{"git://github.com/user/repo.git", "mybranch", "mydir/mysubdir/"}, dir)

	dir, err = parseRemoteURL("https://github.com/user/repo.git")
	require.NoError(t, err)
	assert.NotEmpty(t, dir)
	assert.Equal(t, gitRepo{"https://github.com/user/repo.git", "master", ""}, dir)

	dir, err = parseRemoteURL("https://github.com/user/repo.git#mybranch:mydir/mysubdir/")
	require.NoError(t, err)
	assert.NotEmpty(t, dir)
	assert.Equal(t, gitRepo{"https://github.com/user/repo.git", "mybranch", "mydir/mysubdir/"}, dir)

	dir, err = parseRemoteURL("git@github.com:user/repo.git")
	require.NoError(t, err)
	assert.NotEmpty(t, dir)
	assert.Equal(t, gitRepo{"git@github.com:user/repo.git", "master", ""}, dir)

	dir, err = parseRemoteURL("git@github.com:user/repo.git#mybranch:mydir/mysubdir/")
	require.NoError(t, err)
	assert.NotEmpty(t, dir)
	assert.Equal(t, gitRepo{"git@github.com:user/repo.git", "mybranch", "mydir/mysubdir/"}, dir)
}

func TestCloneArgsSmartHttp(t *testing.T) {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	serverURL, _ := url.Parse(server.URL)

	serverURL.Path = "/repo.git"

	mux.HandleFunc("/repo.git/info/refs", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("service")
		w.Header().Set("Content-Type", fmt.Sprintf("application/x-%s-advertisement", q))
	})

	args := fetchArgs(serverURL.String(), "master")
	exp := []string{"fetch", "--depth", "1", "origin", "master"}
	assert.Equal(t, exp, args)
}

func TestCloneArgsDumbHttp(t *testing.T) {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	serverURL, _ := url.Parse(server.URL)

	serverURL.Path = "/repo.git"

	mux.HandleFunc("/repo.git/info/refs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
	})

	args := fetchArgs(serverURL.String(), "master")
	exp := []string{"fetch", "origin", "master"}
	assert.Equal(t, exp, args)
}

func TestCloneArgsGit(t *testing.T) {
	args := fetchArgs("git://github.com/docker/docker", "master")
	exp := []string{"fetch", "--depth", "1", "origin", "master"}
	assert.Equal(t, exp, args)
}

func gitGetConfig(name string) string {
	b, err := git([]string{"config", "--get", name}...)
	if err != nil {
		// since we are interested in empty or non empty string,
		// we can safely ignore the err here.
		return ""
	}
	return strings.TrimSpace(string(b))
}

func TestCheckoutGit(t *testing.T) {
	root, err := ioutil.TempDir("", "docker-build-git-checkout")
	require.NoError(t, err)
	defer os.RemoveAll(root)

	autocrlf := gitGetConfig("core.autocrlf")
	if !(autocrlf == "true" || autocrlf == "false" ||
		autocrlf == "input" || autocrlf == "") {
		t.Logf("unknown core.autocrlf value: \"%s\"", autocrlf)
	}
	eol := "\n"
	if autocrlf == "true" {
		eol = "\r\n"
	}

	gitDir := filepath.Join(root, "repo")
	_, err = git("init", gitDir)
	require.NoError(t, err)

	_, err = gitWithinDir(gitDir, "config", "user.email", "test@docker.com")
	require.NoError(t, err)

	_, err = gitWithinDir(gitDir, "config", "user.name", "Docker test")
	require.NoError(t, err)

	err = ioutil.WriteFile(filepath.Join(gitDir, "Dockerfile"), []byte("FROM scratch"), 0644)
	require.NoError(t, err)

	subDir := filepath.Join(gitDir, "subdir")
	require.NoError(t, os.Mkdir(subDir, 0755))

	err = ioutil.WriteFile(filepath.Join(subDir, "Dockerfile"), []byte("FROM scratch\nEXPOSE 5000"), 0644)
	require.NoError(t, err)

	if runtime.GOOS != "windows" {
		if err = os.Symlink("../subdir", filepath.Join(gitDir, "parentlink")); err != nil {
			t.Fatal(err)
		}

		if err = os.Symlink("/subdir", filepath.Join(gitDir, "absolutelink")); err != nil {
			t.Fatal(err)
		}
	}

	_, err = gitWithinDir(gitDir, "add", "-A")
	require.NoError(t, err)

	_, err = gitWithinDir(gitDir, "commit", "-am", "First commit")
	require.NoError(t, err)

	_, err = gitWithinDir(gitDir, "checkout", "-b", "test")
	require.NoError(t, err)

	err = ioutil.WriteFile(filepath.Join(gitDir, "Dockerfile"), []byte("FROM scratch\nEXPOSE 3000"), 0644)
	require.NoError(t, err)

	err = ioutil.WriteFile(filepath.Join(subDir, "Dockerfile"), []byte("FROM busybox\nEXPOSE 5000"), 0644)
	require.NoError(t, err)

	_, err = gitWithinDir(gitDir, "add", "-A")
	require.NoError(t, err)

	_, err = gitWithinDir(gitDir, "commit", "-am", "Branch commit")
	require.NoError(t, err)

	_, err = gitWithinDir(gitDir, "checkout", "master")
	require.NoError(t, err)

	// set up submodule
	subrepoDir := filepath.Join(root, "subrepo")
	_, err = git("init", subrepoDir)
	require.NoError(t, err)

	_, err = gitWithinDir(subrepoDir, "config", "user.email", "test@docker.com")
	require.NoError(t, err)

	_, err = gitWithinDir(subrepoDir, "config", "user.name", "Docker test")
	require.NoError(t, err)

	err = ioutil.WriteFile(filepath.Join(subrepoDir, "subfile"), []byte("subcontents"), 0644)
	require.NoError(t, err)

	_, err = gitWithinDir(subrepoDir, "add", "-A")
	require.NoError(t, err)

	_, err = gitWithinDir(subrepoDir, "commit", "-am", "Subrepo initial")
	require.NoError(t, err)

	cmd := exec.Command("git", "submodule", "add", subrepoDir, "sub") // this command doesn't work with --work-tree
	cmd.Dir = gitDir
	require.NoError(t, cmd.Run())

	_, err = gitWithinDir(gitDir, "add", "-A")
	require.NoError(t, err)

	_, err = gitWithinDir(gitDir, "commit", "-am", "With submodule")
	require.NoError(t, err)

	type singleCase struct {
		frag      string
		exp       string
		fail      bool
		submodule bool
	}

	cases := []singleCase{
		{"", "FROM scratch", false, true},
		{"master", "FROM scratch", false, true},
		{":subdir", "FROM scratch" + eol + "EXPOSE 5000", false, false},
		{":nosubdir", "", true, false},   // missing directory error
		{":Dockerfile", "", true, false}, // not a directory error
		{"master:nosubdir", "", true, false},
		{"master:subdir", "FROM scratch" + eol + "EXPOSE 5000", false, false},
		{"master:../subdir", "", true, false},
		{"test", "FROM scratch" + eol + "EXPOSE 3000", false, false},
		{"test:", "FROM scratch" + eol + "EXPOSE 3000", false, false},
		{"test:subdir", "FROM busybox" + eol + "EXPOSE 5000", false, false},
	}

	if runtime.GOOS != "windows" {
		// Windows GIT (2.7.1 x64) does not support parentlink/absolutelink. Sample output below
		// 	git --work-tree .\repo --git-dir .\repo\.git add -A
		//	error: readlink("absolutelink"): Function not implemented
		// 	error: unable to index file absolutelink
		// 	fatal: adding files failed
		cases = append(cases, singleCase{frag: "master:absolutelink", exp: "FROM scratch" + eol + "EXPOSE 5000", fail: false})
		cases = append(cases, singleCase{frag: "master:parentlink", exp: "FROM scratch" + eol + "EXPOSE 5000", fail: false})
	}

	for _, c := range cases {
		ref, subdir := getRefAndSubdir(c.frag)
		r, err := cloneGitRepo(gitRepo{remote: gitDir, ref: ref, subdir: subdir})

		if c.fail {
			assert.Error(t, err)
			continue
		} else {
			require.NoError(t, err)
			if c.submodule {
				b, err := ioutil.ReadFile(filepath.Join(r, "sub/subfile"))
				require.NoError(t, err)
				assert.Equal(t, "subcontents", string(b))
			} else {
				_, err := os.Stat(filepath.Join(r, "sub/subfile"))
				require.Error(t, err)
				require.True(t, os.IsNotExist(err))
			}
		}

		b, err := ioutil.ReadFile(filepath.Join(r, "Dockerfile"))
		require.NoError(t, err)
		assert.Equal(t, c.exp, string(b))
	}
}

func TestValidGitTransport(t *testing.T) {
	gitUrls := []string{
		"git://github.com/docker/docker",
		"git@github.com:docker/docker.git",
		"git@bitbucket.org:atlassianlabs/atlassian-docker.git",
		"https://github.com/docker/docker.git",
		"http://github.com/docker/docker.git",
		"http://github.com/docker/docker.git#branch",
		"http://github.com/docker/docker.git#:dir",
	}
	incompleteGitUrls := []string{
		"github.com/docker/docker",
	}

	for _, url := range gitUrls {
		if !isGitTransport(url) {
			t.Fatalf("%q should be detected as valid Git prefix", url)
		}
	}

	for _, url := range incompleteGitUrls {
		if isGitTransport(url) {
			t.Fatalf("%q should not be detected as valid Git prefix", url)
		}
	}
}
