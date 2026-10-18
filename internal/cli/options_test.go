package cli

import "testing"

func TestNormalizeOptionsUsesPOSIXGNUConvention(t *testing.T) {
	short := map[string]struct{}{"v": {}}
	long := map[string]struct{}{"run": {}}
	got, err := NormalizeOptions([]string{"-v", "--run", "IndexTest"}, short, long)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-v", "-run", "IndexTest"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("normalized = %#v, want %#v", got, want)
		}
	}
}
func TestNormalizeOptionsRejectsReversedDashConvention(t *testing.T) {
	short := map[string]struct{}{"v": {}}
	long := map[string]struct{}{"run": {}}
	for _, args := range [][]string{{"--v"}, {"-run", "IndexTest"}} {
		if _, err := NormalizeOptions(args, short, long); err == nil {
			t.Fatalf("arguments %q were accepted", args)
		}
	}
}

func TestNormalizeOptionsSupportsLongEqualsSyntax(t *testing.T) {
	long := map[string]struct{}{"run": {}}
	got, err := NormalizeOptions([]string{"--run=IndexTest"}, nil, long)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "-run=IndexTest" {
		t.Fatalf("normalized = %#v", got)
	}
}

func TestNormalizeOptionsStopsAtDoubleDash(t *testing.T) {
	short := map[string]struct{}{"v": {}}
	long := map[string]struct{}{"run": {}}
	got, err := NormalizeOptions([]string{"-v", "--", "-run", "--v"}, short, long)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-v", "--", "-run", "--v"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("normalized = %#v, want %#v", got, want)
		}
	}
}

func TestNormalizeOptionsReportsPublicSpelling(t *testing.T) {
	short := map[string]struct{}{"v": {}}
	long := map[string]struct{}{"run": {}}

	_, err := NormalizeOptions([]string{"--v"}, short, long)
	if err == nil || err.Error() != "invalid option --v: short option must use one dash (-v)" {
		t.Fatalf("error = %v", err)
	}
	_, err = NormalizeOptions([]string{"-run"}, short, long)
	if err == nil || err.Error() != "invalid option -run: long option must use two dashes (--run)" {
		t.Fatalf("error = %v", err)
	}
}

func TestNormalizeOptionsRejectsUnknownOptionsBeforeFlagPackage(t *testing.T) {
	short := map[string]struct{}{"v": {}}
	long := map[string]struct{}{"run": {}}
	for _, option := range []string{"--unknown", "--helplplp", "-x"} {
		_, err := NormalizeOptions([]string{option}, short, long)
		if err == nil || err.Error() != "unknown option: "+option {
			t.Fatalf("option %q error = %v", option, err)
		}
	}
}
