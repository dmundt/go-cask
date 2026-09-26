package toolchain

import "testing"

func TestBinDir(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		gobin  string
		gopath string
		want   string
	}{
		{name: "GOBIN wins", gobin: "/opt/bin", gopath: "/home/me/go", want: "/opt/bin"},
		{name: "GOPATH's bin when GOBIN is unset", gopath: "/home/me/go", want: "/home/me/go/bin"},
		{name: "a trailing separator is not doubled", gopath: "/home/me/go/", want: "/home/me/go/bin"},
		{name: "a Windows GOPATH keeps its form", gopath: `C:\Users\me\go`, want: `C:\Users\me\go/bin`},
		{name: "neither set resolves nothing", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := BinDir(tc.gobin, tc.gopath); got != tc.want {
				t.Errorf("BinDir(%q, %q) = %q, want %q", tc.gobin, tc.gopath, got, tc.want)
			}
		})
	}
}

func TestNeedsMount(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		goos      string
		osrelease string
		want      bool
	}{
		{name: "WSL reports the Microsoft kernel", goos: "linux", osrelease: "5.15.90.1-microsoft-standard-WSL2", want: true},
		{name: "a Windows kernel name is enough", goos: "linux", osrelease: "4.4.0-19041-Microsoft", want: true},
		{name: "a plain Linux kernel", goos: "linux", osrelease: "6.8.0-45-generic"},
		{name: "no translation on Windows itself", goos: "windows", osrelease: "10.0.22631"},
		{name: "no translation on macOS", goos: "darwin", osrelease: "23.6.0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := NeedsMount(tc.goos, tc.osrelease); got != tc.want {
				t.Errorf("NeedsMount(%q, %q) = %v, want %v", tc.goos, tc.osrelease, got, tc.want)
			}
		})
	}
}

func TestMountPath(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		value string
		want  string
	}{
		{name: "a backslash drive path", value: `C:\Users\me\go\bin`, want: "/mnt/c/Users/me/go/bin"},
		{name: "a forward-slash drive path", value: "C:/Users/me/go/bin", want: "/mnt/c/Users/me/go/bin"},
		{name: "a lower-case drive keeps one form", value: "d:/src/go/bin", want: "/mnt/d/src/go/bin"},
		{name: "a POSIX path is unchanged", value: "/home/me/go/bin", want: "/home/me/go/bin"},
		{name: "a relative path is not a drive", value: "go/bin", want: "go/bin"},
		{name: "a single character is not a drive", value: "C:", want: "/mnt/c"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := MountPath(tc.value); got != tc.want {
				t.Errorf("MountPath(%q) = %q, want %q", tc.value, got, tc.want)
			}
		})
	}
}

func TestCandidates(t *testing.T) {
	t.Parallel()

	got := Candidates("govulncheck")
	if len(got) != 2 || got[0] != "govulncheck.exe" || got[1] != "govulncheck" {
		t.Errorf("Candidates = %q, want the Windows executable first and the plain name second", got)
	}
}

func TestScannerVersion(t *testing.T) {
	t.Parallel()

	const report = "Scanner: govulncheck@v1.8.0\n" +
		"DB: https://vuln.go.dev\n" +
		"Go: go1.27.1\n"

	cases := []struct {
		name   string
		report string
		want   string
	}{
		{name: "the scanner line", report: report, want: "v1.8.0"},
		{name: "trailing space is trimmed", report: "Scanner: govulncheck@v1.8.0  \n", want: "v1.8.0"},
		{name: "another tool names no scanner", report: "govulncheck: development build\n", want: ""},
		{name: "an empty report", report: "", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ScannerVersion(tc.report, "govulncheck"); got != tc.want {
				t.Errorf("ScannerVersion = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPinned(t *testing.T) {
	t.Parallel()

	const report = "Scanner: govulncheck@v1.8.0\n"

	cases := []struct {
		name    string
		report  string
		version string
		want    bool
	}{
		{name: "the pinned version", report: report, version: "v1.8.0", want: true},
		{name: "another version", report: report, version: "v1.9.0", want: false},
		{name: "no version asked for", report: report, want: false},
		{name: "a report that names none", report: "not a scanner\n", version: "v1.8.0", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Pinned(tc.report, "govulncheck", tc.version); got != tc.want {
				t.Errorf("Pinned(%q, %q) = %v, want %v", tc.report, tc.version, got, tc.want)
			}
		})
	}
}
