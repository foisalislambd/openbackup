// Command version reads VERSION.md and answers the questions CI asks:
// is it valid semver, is it newer than the tags on stdin, and what -ldflags
// stamp that version into a binary.
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/foisalislambd/openbackup/internal/version"
)

const modulePath = "github.com/foisalislambd/openbackup/internal/version"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	if err := run(os.Args[1], os.Args[2:]); err != nil {
		fmt.Fprintf(os.Stderr, "version: %v\n", err)
		os.Exit(1)
	}
}

func run(cmd string, args []string) error {
	switch cmd {
	case "print", "print-tag", "print-product", "print-notes", "check":
		rel, err := readFile()
		if err != nil {
			return err
		}
		switch cmd {
		case "print":
			fmt.Println(rel.Version.String())
		case "print-tag":
			fmt.Println(rel.Version.Tag())
		case "print-product":
			fmt.Println(rel.Version.Product())
		case "print-notes":
			if rel.Notes != "" {
				fmt.Println(rel.Notes)
			}
		case "check":
			fmt.Printf("VERSION.md %s is valid semver\n", rel.Version.String())
		}
		return nil
	case "ldflags":
		rel, err := readFile()
		if err != nil {
			return err
		}
		flags, err := ldflags(rel.Version)
		if err != nil {
			return err
		}
		fmt.Println(flags)
		return nil
	case "stamp-wails":
		path := "desktop/wails.json"
		if len(args) > 0 {
			path = args[0]
		}
		rel, err := readFile()
		if err != nil {
			return err
		}
		if err := version.StampWailsProductVersion(path, rel.Version.Product()); err != nil {
			return err
		}
		fmt.Printf("wails productVersion=%s\n", rel.Version.Product())
		return nil
	case "cmp":
		if len(args) != 2 {
			return fmt.Errorf("usage: version cmp <a> <b>")
		}
		a, err := version.Parse(args[0])
		if err != nil {
			return err
		}
		b, err := version.Parse(args[1])
		if err != nil {
			return err
		}
		switch c := a.Compare(b); {
		case c < 0:
			fmt.Println("lt")
		case c > 0:
			fmt.Println("gt")
		default:
			fmt.Println("eq")
		}
		return nil
	case "decide":
		rel, err := readFile()
		if err != nil {
			return err
		}
		d := version.DecideRelease(rel.Version, readCandidates(os.Stdin))
		skip := "false"
		if d.Skip {
			skip = "true"
		}
		fmt.Printf("skip=%s\nversion=%s\ntag=%s\nproduct=%s\nmessage=%s\n", skip, d.Version, d.Tag, d.Product, oneLine(d.Message))
		return nil
	case "pr-check":
		changed := false
		for _, a := range args {
			if a == "--changed" {
				changed = true
			}
		}
		rel, err := readFile()
		if err != nil {
			return err
		}
		status, message := version.CheckPullRequest(rel.Version, readCandidates(os.Stdin), changed)
		// Always exit 0 when VERSION.md parses. The workflow fails the job
		// itself when status=fail, so a version that is simply not newer does
		// not look like a broken version file.
		fmt.Printf("status=%s\nmessage=%s\n", status, oneLine(message))
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func readFile() (version.ReleaseFile, error) {
	path := os.Getenv("OPENBACKUP_VERSION_FILE")
	if path == "" {
		path = "VERSION.md"
	}
	return version.ReadReleaseFile(path)
}

func readCandidates(r io.Reader) []string {
	var out []string
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		out = append(out, sc.Text())
	}
	return out
}

func ldflags(v version.SemVer) (string, error) {
	commit := os.Getenv("COMMIT")
	if commit == "" {
		out, err := exec.Command("git", "rev-parse", "--short=7", "HEAD").Output()
		if err != nil {
			commit = "unknown"
		} else {
			commit = strings.TrimSpace(string(out))
		}
	}
	date := os.Getenv("DATE")
	if date == "" {
		date = time.Now().UTC().Format("2006-01-02T15:04:05Z")
	}
	for _, part := range []struct{ name, value string }{
		{"version", v.Tag()},
		{"commit", commit},
		{"date", date},
	} {
		if !safeFlag(part.value) {
			return "", fmt.Errorf("unsafe %s %q", part.name, part.value)
		}
	}
	return fmt.Sprintf("-s -w -X %s.Version=%s -X %s.Commit=%s -X %s.Date=%s",
		modulePath, v.Tag(), modulePath, commit, modulePath, date), nil
}

func safeFlag(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		ok := (c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '.' || c == '+' || c == '-' || c == ':' || c == 'T' || c == 'Z'
		if !ok {
			return false
		}
	}
	return true
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.Join(strings.Fields(s), " ")
}

func usage() {
	fmt.Fprintf(os.Stderr, `Usage: version <command>

  print            bare semver from VERSION.md
  print-tag        v-prefixed tag
  print-product    MAJOR.MINOR.PATCH for Windows
  print-notes      human notes below the version line
  check            exit 1 unless VERSION.md is valid semver
  ldflags          Go -ldflags that stamp this version
  stamp-wails [f]  set productVersion in wails.json (default desktop/wails.json)
  cmp A B          print gt, eq, or lt
  decide           read released tags on stdin; print skip/version/tag/product/message
  pr-check [--changed]
                   read released tags on stdin; print status and message
                   (--changed fails when the version is not a newer release)

OPENBACKUP_VERSION_FILE overrides the path (default VERSION.md).
COMMIT and DATE override the values embedded by ldflags.
`)
}
