package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestParseConfig(t *testing.T) {
	Conf.parse()

	// Test if all fields are filled.
	v := reflect.ValueOf(Conf)
	for i := 0; i < v.NumField(); i++ {
		if v.Field(i).Kind() == reflect.Struct {
			continue
		}
		if v.Field(i).Interface() != nil {
			continue
		}
		t.Fatalf("read empty from config, field: %v", v.Type().Field(i).Name)
	}
}

// TestMissingConfigIsFatal pins that a GORE_CONF naming a file that
// cannot be read stops the server. The configuration is mounted into the
// container, so a wrong path is a realistic mistake, and falling back to
// the embedded sample would quietly point the deployment at a different
// database and re-enable the sample credentials.
func TestMissingConfigIsFatal(t *testing.T) {
	if os.Getenv("GORE_TEST_SUBPROCESS") == "1" {
		// parse() already ran from init. Reaching here means it did not
		// stop, which the parent reports as a failure.
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestMissingConfigIsFatal$")
	cmd.Env = append(os.Environ(),
		"GORE_TEST_SUBPROCESS=1",
		"GORE_CONF="+filepath.Join(t.TempDir(), "absent.yml"),
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("an unreadable GORE_CONF did not stop startup, output:\n%s", out)
	}
	if !strings.Contains(string(out), "cannot read GORE_CONF") {
		t.Errorf("want a message naming GORE_CONF, got:\n%s", out)
	}
}

func TestAdminCanBeClosed(t *testing.T) {
	var c config
	if err := yaml.Unmarshal([]byte("admin:\n  enable: false\n"), &c); err != nil {
		t.Fatal(err)
	}
	if err := c.checkAdmin(); err != nil {
		t.Fatal(err)
	}
	if c.A.Enable {
		t.Fatal("admin.enable false left the console on")
	}
}

func TestAdminPrefixMustBeAPrefix(t *testing.T) {
	c := config{}
	c.S.Prefix = "/s/"
	c.X.Prefix = "/x/"
	c.A.Enable = true
	c.A.Prefix = "/a"
	if err := c.checkAdmin(); err == nil {
		t.Fatal("a prefix without a trailing slash was accepted")
	}
}

func TestAdminPrefixMustNotCollide(t *testing.T) {
	for _, prefix := range []string{"/s/", "/x/", "/s/a/"} {
		c := config{}
		c.S.Prefix = "/s/"
		c.X.Prefix = "/x/"
		c.A.Enable = true
		c.A.Prefix = prefix
		if err := c.checkAdmin(); err == nil {
			t.Errorf("admin.prefix %q was accepted", prefix)
		}
	}
}
