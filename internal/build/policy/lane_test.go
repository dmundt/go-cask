package policy

import (
	"path"
	"strings"
	"testing"
)

// TestLandLaneTableIsWellFormed pins the slot's layout: the record names are bare file
// names, the window is a positive number of minutes, and the override is spelled as an
// environment variable.
func TestLandLaneTableIsWellFormed(t *testing.T) {
	t.Parallel()

	table := LandLane()
	for _, field := range []struct {
		name  string
		value string
	}{
		{"Dir", table.Dir},
		{"Owner", table.Owner},
		{"RepoID", table.RepoID},
		{"Takeover", table.Takeover},
		{"Token", table.Token},
		{"StaleEnv", table.StaleEnv},
		{"DeadGraceEnv", table.DeadGraceEnv},
		{"WaitEnv", table.WaitEnv},
	} {
		if field.value == "" {
			t.Errorf("%s is empty", field.name)
		}
		if path.Base(field.value) != field.value && field.name != "Dir" {
			t.Errorf("%s = %q, want a bare file name", field.name, field.value)
		}
	}
	for _, name := range []string{table.Owner, table.RepoID, table.Takeover} {
		if strings.ContainsAny(name, `/\`) {
			t.Errorf("the record %q is a path; the records are named inside Dir", name)
		}
	}
	if table.StaleMinutes <= 0 {
		t.Errorf("the idle window is %d minutes, want a positive number", table.StaleMinutes)
	}
	if table.StaleEnv != strings.ToUpper(table.StaleEnv) {
		t.Errorf("the override %q is not spelled as an environment variable", table.StaleEnv)
	}
	// The window's default is quoted in prose a reader relies on; a change here is a
	// change to the documented contract, so it is pinned rather than merely validated.
	if table.StaleMinutes != 90 {
		t.Errorf("the idle window is %d minutes, want the documented 90", table.StaleMinutes)
	}
	if table.DeadGraceSeconds <= 0 {
		t.Errorf("the dead-holder grace is %ds, want a positive number", table.DeadGraceSeconds)
	}
	if table.DeadGraceEnv != strings.ToUpper(table.DeadGraceEnv) {
		t.Errorf("the override %q is not spelled as an environment variable", table.DeadGraceEnv)
	}
	// The grace answers a different question from the window — how long to wait for the machine
	// to finish noticing a holder is gone, rather than how long to wait for a holder that might
	// still be alive — so it must stay far shorter, or --takeover-dead would buy nothing.
	if table.DeadGraceSeconds >= table.StaleMinutes*60 {
		t.Errorf("the dead-holder grace (%ds) is not shorter than the idle window (%dm)",
			table.DeadGraceSeconds, table.StaleMinutes)
	}
	if table.DeadGraceSeconds != 60 {
		t.Errorf("the dead-holder grace is %ds, want the documented 60", table.DeadGraceSeconds)
	}
	if table.WaitSeconds <= 0 {
		t.Errorf("the default wait is %ds, want a positive number", table.WaitSeconds)
	}
	if table.WaitEnv != strings.ToUpper(table.WaitEnv) {
		t.Errorf("the override %q is not spelled as an environment variable", table.WaitEnv)
	}
	// The wait is a queue for a gate run, so its default has to be long enough to outlast one:
	// a default shorter than the run it is waiting for would expire while the holder is working.
	if table.WaitSeconds < 300 {
		t.Errorf("the default wait is %ds, too short to outlast a gate run", table.WaitSeconds)
	}
	if table.WaitSeconds != 600 {
		t.Errorf("the default wait is %ds, want the documented 600", table.WaitSeconds)
	}
}

// TestLandLaneSlotIsNeverCommitted pins that the slot is a local record: it lives in the
// shared git directory, which is not tracked, so no path carrying its directory may be
// committed. A tracked slot would make one clone's timing another clone's business.
func TestLandLaneSlotIsNeverCommitted(t *testing.T) {
	t.Parallel()

	tracked := gitList(t, repoRoot(t), "ls-files")
	for _, file := range tracked {
		if strings.Contains(file, LandLane().Dir) {
			t.Errorf("%s is committed; the advisory slot is a local record", file)
		}
	}
}
