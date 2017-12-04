package flagutil

import (
	"flag"
	"os"
	"testing"
)

func TestPopulate(t *testing.T) {

	var (
		fs  = flag.NewFlagSet("testing", flag.ExitOnError)
		foo = fs.String("foo", "", "")
		bar = fs.String("bar", "", "")
		baz = fs.String("baz", "", "")
	)
	fs.Parse([]string{"-foo=foo", "-baz=baz"})

	os.Clearenv()
	os.Setenv("DOORMAN_BAR", "bar")
	os.Setenv("DOORMAN_BAZ", "baz from env")

	if err := Populate(fs, "DOORMAN"); err != nil {
		t.Fatal(err)
	}

	if got, want := *foo, "foo"; got != want {
		t.Errorf("foo=%q; want %q", got, want)
	}
	if got, want := *bar, "bar"; got != want {
		t.Errorf("bar=%q; want %q", got, want)
	}
	if got, want := *baz, "baz"; got != want {
		t.Errorf("baz=%q; want %q", got, want)
	}
}
