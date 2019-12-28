package events

import (
	"math"
	"time"
)

// These types are based on https://gerrit-review.googlesource.com/Documentation/json.html .
// Note that although they represent much the same things, these types are not the same as the
// types used by the REST API, which can be found in gerrit/types.go. These two classes of
// type are, for the most part, wholly incompatible with each other.

// UnixFloatTime translates a float64 UNIX epoch time to a time.Time.
func UnixFloatTime(floatTime float64) time.Time {
	return time.Unix(int64(floatTime), int64((floatTime-math.Floor(floatTime))*1e9))
}

// Change refers to a change being reviewed, or that was already reviewed.
type Change struct {
	// Project specifies the project path within Gerrit.
	Project string
	// Branch is the branch name within the project.
	Branch string
	// Topic is the name specified by the uploader for this change series.
	Topic string
	// ID gives the Gerrit Change-ID.
	ID string
	// Number is the (deprecated) change number.
	Number int
	// Subject is the description of a change.
	Subject string
	// Owner is the owner of a change series.
	Owner Account
	// URL gives the canonical URL to reach this change.
	URL string
	// CommitMessage gives the full commit message for the current patchset.
	CommitMessage string
	// CreatedOn gives the time since the UNIX epoch when this change was created.
	CreatedOn int64
	// LastUpdated gives the time since the UNIX epoch when this change was last updated.
	LastUpdated int64
	// Open indicates whether the change is still open for review.
	Open bool
	// Status indicates the current state of this change ("NEW"/"MERGED"/"ABANDONED").
	Status string
	// Comments gives all inline/file comments for this change.
	Comments []Message
	// TrackingIDs gives all issue tracking system links, as scraped out of the commit
	// message based on the server's 'trackingid' sections.
	TrackingIDs []struct {
		// System is the name of the system as given in the gerrit.config file.
		System string
		// ID is the ID number as scraped.
		ID string
	}
	// CurrentPatchSet gives the current patchset for this change.
	CurrentPatchSet PatchSet
	// PatchSets holds all patchsets for this change.
	PatchSets []PatchSet
	// DependsOn is a list of changes on which this change depends.
	DependsOn []Dependency
	// NeededBy is a list of changes which depend on this change.
	NeededBy []Dependency
	// SubmitRecords has information on whether this change has been or can be submitted.
	SubmitRecords []SubmitRecord
	// AllReviewers is a list of reviewers added to this change.
	AllReviewers []Account
}

// Account is a Gerrit user account.
type Account struct {
	// Name is the user's full name, if configured.
	Name string
	// Email is the user's preferred email address.
	Email string
	// Username is the user's username, if configured.
	Username string
}

// PatchSet refers to a specific patchset within a change.
type PatchSet struct {
	// Number is the patchset number.
	Number int
	// Revision is the git commit for this patchset.
	Revision string
	// Parents is the list of parent revisions.
	Parents []string
	// Ref is the git reference pointing at the revision. This reference is available through the
	// Gerrit Code Review server's Git interface for the containing change.
	Ref string
	// Uploader is the uploader of the patchset.
	Uploader Account
	// Author is the author of the patchset.
	Author Account
	// CreatedOn is the time in seconds since the UNIX epoch when this patchset was created.
	CreatedOn int64
	// Kind is the kind of change uploaded ("REWORK"/"TRIVIAL_REBASE"/"MERGE_FIRST_PARENT_UPDATE"/
	// "NO_CODE_CHANGE"/"NO_CHANGE").
	Kind string
	// Approvals are the approvals granted to the patchset.
	Approvals []Approval
	// Comments gives all comments for this patchset.
	Comments []PatchSetComment
	// Files gives all changed files in this patchset.
	Files []FilePatch
	// SizeInsertions gives size information of insertions of this patchset.
	SizeInsertions int
	// SizeDeletions gives size information of deletions of this patchset.
	SizeDeletions int
}

// Approval records a code review approval granted to a patchset.
type Approval struct {
	// Type is the internal name of the approval given.
	Type string
	// Description is the human readable category of the approval.
	Description string
	// Value is the value assigned by the approval, usually a numerical score.
	Value string
	// OldValue is the previous approval score, usually a numerical score.
	OldValue int
	// GrantedOn is the time in seconds since the UNIX epoch when this approval was added or last
	// updated.
	GrantedOn int64
	// By is the reviewer of the patchset.
	By Account
}

// RefUpdate is information about a ref that was updated.
type RefUpdate struct {
	// OldRev is the old value of the ref, prior to the update.
	OldRev string
	// NewRev is the new value the ref was updated to. Zero value
	// ("0000000000000000000000000000000000000000") indicates that the ref was deleted.
	NewRev string
	// RefName is the full rev name within project.
	RefName string
	// Project is the project path in Gerrit.
	Project string
}

// SubmitRecord is information about the submit status of a change.
type SubmitRecord struct {
	// Status is the current changeset submit status ("OK"/"NOT_READY"/"RULE_ERROR").
	Status string
	// Labels describes the state of each code review label, unless the status is RULE_ERROR.
	Labels []Label
	// Requirements describes what needs to be changed in order for the change to be submittable.
	Requirements []Requirement
}

// Requirement gives information about a requirement in order to submit a change.
type Requirement struct {
	// FallbackText is a human readable description of the requirement.
	FallbackText string
	// Type is an alphanumerical (plus hyphens or underscores) string to identify what the
	// requirement is and why is was triggered. Can be seen as a class: requirements sharing the
	// same type were created for a similar reason, and the data structure will follow one set of
	// rules.
	Type string
	// Data is additional key-value data linked to this requirement. This is used in templates to
	// render rich status messages.
	Data map[string]string
}

// Label contains information about a code review label for a change.
type Label struct {
	// Label is the name of the label.
	Label string
	// Status is the status of the label ("OK"/"REJECT"/"NEED"/"MAY"/"IMPOSSIBLE").
	Status string
	// By is the account that applied the label.
	By Account
}

// Dependency is information about a change or patchset dependency.
type Dependency struct {
	// ID is the change identifier.
	ID string
	// Number is the (deprecated) change number.
	Number int
	// Revision is the patchset revision.
	Revision string
	// Ref is the ref name.
	Ref string
	// IsCurrentPatchSet indicates if the revision is the current patchset of the change.
	IsCurrentPatchSet bool
}

// Message is a comment added on a change by a reviewer.
type Message struct {
	// Timestamp is the time in seconds since the UNIX epoch when this comment was added.
	Timestamp int64
	// Reviewer is the account that added the comment.
	Reviewer Account
	// Message is the comment text.
	Message string
}

// PatchSetComment is a comment added on a patchset by a reviewer.
type PatchSetComment struct {
	// File is the name of the file on which the comment was added.
	File string
	// Line is the line number at which the comment was added.
	Line int
	// Reviewer is the account that added the comment.
	Reviewer Account
	// Message is the comment text.
	Message string
}

// FilePatch is information about a patch on a file.
type FilePatch struct {
	// File is the name of the file. If the file is renamed, the new name.
	File string
	// FileOld is the old name of the file, if the file is renamed.
	FileOld string
	// Type is the type of change ("ADDED"/"MODIFIED"/"DELETED"/"RENAMED"/"COPIED"/"REWRITE")
	Type string
	// Insertions is the number of insertions of this patch.
	Insertions int
	// Deletions is the number of deletions of this patch.
	Deletions int
}
