package version

import "fmt"

// Decision is what the release workflow should do with VERSION.md.
type Decision struct {
	Skip    bool
	Version string
	Tag     string
	Product string
	Message string
}

// DecideRelease compares the version in VERSION.md with every already
// released tag. A release is produced only when current is strictly newer
// than the highest semver among candidates. Unparsable candidates are ignored.
func DecideRelease(current SemVer, candidates []string) Decision {
	d := Decision{
		Version: current.String(),
		Tag:     current.Tag(),
		Product: current.Product(),
	}
	best, ok := Highest(candidates)
	if !ok {
		d.Message = fmt.Sprintf("No released version found. Releasing %s from VERSION.md.", d.Tag)
		return d
	}
	if current.Compare(best) > 0 {
		d.Message = fmt.Sprintf("VERSION.md %s is greater than the latest released version %s. Releasing %s.", d.Version, best.String(), d.Tag)
		return d
	}
	d.Skip = true
	d.Message = fmt.Sprintf("Release skipped: VERSION.md is %s, which is not greater than the latest released version %s. Bump VERSION.md to a higher semver to publish a release.", d.Version, best.String())
	return d
}

// CheckPullRequest reports whether a pull request's VERSION.md is valid to merge.
// status is "ok", "warn", or "fail".
//
// An invalid file is rejected by the caller before this runs. A version that
// is not greater than the latest release fails the check when the version
// number itself changed (the PR is trying to set a release version that will
// not publish). An unchanged version only warns, so ordinary pull requests
// can merge without cutting a release.
func CheckPullRequest(current SemVer, candidates []string, versionChanged bool) (status, message string) {
	best, ok := Highest(candidates)
	if !ok || current.Compare(best) > 0 {
		if !ok {
			return "ok", fmt.Sprintf("VERSION.md %s is valid and there is no prior release. Merging to main will publish %s.", current.String(), current.Tag())
		}
		return "ok", fmt.Sprintf("VERSION.md %s is greater than the latest released version %s. Merging to main will publish %s.", current.String(), best.String(), current.Tag())
	}
	if versionChanged {
		return "fail", fmt.Sprintf("VERSION.md was changed to %s, which is not greater than the latest released version %s. Set a strictly higher semver to cut a release, or leave the released version unchanged.", current.String(), best.String())
	}
	return "warn", fmt.Sprintf("VERSION.md is %s, which is not greater than the latest released version %s. This pull request will not publish a release. Bump VERSION.md when you intend to release.", current.String(), best.String())
}
