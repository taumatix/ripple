package platform

// RepoHead is one repository's default-branch head as a host with installations reports it
// (a GitHub App): the repo, the installation it is reached through, its default branch and the
// commit that branch points at. Head is empty for a repository with no commits.
type RepoHead struct {
	FullName       string
	InstallationID int64
	DefaultBranch  string
	Head           string
}
