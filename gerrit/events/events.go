package events

import (
	"encoding/json"
	"math"
	"time"

	"github.com/zeebo/errs"
)

var (
	EventDecodingError = errs.Class("event decoding error")
)

// UnixFloatTime translates a float64 UNIX epoch time to a time.Time.
func UnixFloatTime(floatTime float64) time.Time {
	return time.Unix(int64(floatTime), int64((floatTime-math.Floor(floatTime))*1e9))
}

// GerritEvent is a common interface to various gerrit event structures.
type GerritEvent interface {
	GetType() string
	EventCreatedAt() time.Time
}

// GerritEventBase is a base for gerrit event types, providing the necessary methods for
// GerritEvent compatibility.
type GerritEventBase struct {
	Type           string
	EventCreatedOn float64 `json:"eventCreatedOn"`
}

func (g *GerritEventBase) GetType() string {
	return g.Type
}

func (g *GerritEventBase) EventCreatedAt() time.Time {
	return UnixFloatTime(g.EventCreatedOn)
}

func DecodeGerritEvent(eventJSON []byte) (GerritEvent, error) {
	var eventType GerritEventBase
	if err := json.Unmarshal(eventJSON, &eventType); err != nil {
		return nil, EventDecodingError.Wrap(err)
	}
	var evStruct interface{}
	// lol yes we are just going to unmarshal it again
	switch eventType.Type {
	case "assignee-changed":
		evStruct = &AssigneeChangedEvent{}
	case "change-abandoned":
		evStruct = &ChangeAbandonedEvent{}
	case "change-merged":
		evStruct = &ChangeMergedEvent{}
	case "change-restored":
		evStruct = &ChangeRestoredEvent{}
	case "comment-added":
		evStruct = &CommentAddedEvent{}
	case "dropped-output":
		evStruct = &DroppedOutputEvent{}
	case "hashtags-changed":
		evStruct = &HashtagsChangedEvent{}
	case "project-created":
		evStruct = &ProjectCreatedEvent{}
	case "patchset-created":
		evStruct = &PatchSetCreatedEvent{}
	case "ref-updated":
		evStruct = &RefUpdatedEvent{}
	case "reviewer-added":
		evStruct = &ReviewerAddedEvent{}
	case "reviewer-deleted":
		evStruct = &ReviewerDeletedEvent{}
	case "topic-changed":
		evStruct = &TopicChangedEvent{}
	case "vote-deleted":
		evStruct = &VoteDeletedEvent{}
	default:
		return nil, EventDecodingError.New("unrecognized event type %q", eventType.Type)
	}
	if err := json.Unmarshal(eventJSON, evStruct); err != nil {
		return nil, EventDecodingError.Wrap(err)
	}
	return evStruct.(GerritEvent), nil
}

// GerritUser is a Gerrit user account.
type GerritUser struct {
	// Name is the user's full name, if configured.
	Name string
	// Email is the user's preferred email address.
	Email string
	// Username is the user's username, if configured.
	Username string
}

// GerritMessage is a comment added on a change by a reviewer.
type GerritMessage struct {
	// Timestamp is the time in seconds since the UNIX epoch when this comment was added.
	Timestamp float64
	// Reviewer is the account that added the comment.
	Reviewer GerritUser
	// Message is the comment text.
	Message string
}

// GerritApproval records a code review approval granted to a patchset.
type GerritApproval struct {
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
	GrantedOn float64
	// By is the reviewer of the patchset.
	By GerritUser
}

// GerritPatchSet refers to a specific patchset within a change.
type GerritPatchSet struct {
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
	Uploader GerritUser
	// Author is the author of the patchset.
	Author GerritUser
	// CreatedOn is the time in seconds since the UNIX epoch when this patchset was created.
	CreatedOn float64
	// Kind is the kind of change uploaded ("REWORK"/"TRIVIAL_REBASE"/"MERGE_FIRST_PARENT_UPDATE"/
	// "NO_CODE_CHANGE"/"NO_CHANGE").
	Kind string
	// Approvals are the approvals granted to the patchset.
	Approvals []GerritApproval
}

// GerritDependency is information about a change or patchset dependency.
type GerritDependency struct {
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

// GerritLabel contains information about a code review label on a specific change.
type GerritLabel struct {
	// Label is the name of the label.
	Label string
	// Status is the status of the label ("OK"/"REJECT"/"NEED"/"MAY"/"IMPOSSIBLE").
	Status string
	// By is the account that applied the label.
	By GerritUser
}

// GerritChange refers to a change being reviewed, or that was already reviewed.
type GerritChange struct {
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
	Owner GerritUser
	// URL gives the canonical URL to reach this change.
	URL string
	// CommitMessage gives the full commit message for the current patchset.
	CommitMessage string
	// CreatedOn gives the time since the UNIX epoch when this change was created.
	CreatedOn float64
	// LastUpdated gives the time since the UNIX epoch when this change was last updated.
	LastUpdated float64
	// Open indicates whether the change is still open for review.
	Open bool
	// Status indicates the current state of this change ("NEW"/"MERGED"/"ABANDONED").
	Status string
	// Comments gives all inline/file comments for this change.
	Comments []GerritMessage
	// TrackingIDs gives all issue tracking system links, as scraped out of the commit
	// message based on the server's 'trackingid' sections.
	TrackingIDs []struct {
		// System is the name of the system as given in the gerrit.config file.
		System string
		// ID is the ID number as scraped.
		ID string
	}
	// CurrentPatchSet gives the current patchset for this change.
	CurrentPatchSet GerritPatchSet
	// PatchSets holds all patchsets for this change.
	PatchSets []GerritPatchSet
	// DependsOn is a list of changes on which this change depends.
	DependsOn []GerritDependency
	// NeededBy is a list of changes which depend on this change.
	NeededBy []GerritDependency
	// SubmitRecords has information on whether this change has been or can be submitted.
	SubmitRecords struct {
		// Status is the current changeset submit status ("OK"/"NOT_READY"/"RULE_ERROR").
		Status string
		// Labels describes the state of each code review label, unless the status is RULE_ERROR.
		Labels []GerritLabel
	}
	// AllReviewers is a list of reviewers added to this change.
	AllReviewers []GerritUser
}

// AssigneeChangedEvent is sent when the assignee of a change has been modified.
type AssigneeChangedEvent struct {
	GerritEventBase
	Change      GerritChange
	Changer     GerritUser
	OldAssignee GerritUser
}

// ChangeAbandonedEvent is sent when a change has been abandoned.
type ChangeAbandonedEvent struct {
	GerritEventBase
	Change    GerritChange
	PatchSet  GerritPatchSet
	Abandoner GerritUser
	Reason    string
}

// ChangeMergedEvent is sent when a change has been merged into the git repository.
type ChangeMergedEvent struct {
	GerritEventBase
	Change    GerritChange
	PatchSet  GerritPatchSet
	Submitter GerritUser
	NewRev    string
}

// ChangeRestoredEvent is sent when an abandoned change has been restored.
type ChangeRestoredEvent struct {
	GerritEventBase
	Change   GerritChange
	PatchSet GerritPatchSet
	Restorer GerritUser
	Reason   string
}

// CommentAddedEvent is sent when a review comment has been posted on a change.
type CommentAddedEvent struct {
	GerritEventBase
	Change    GerritChange
	PatchSet  GerritPatchSet
	Author    GerritUser
	Approvals []GerritApproval
	Comment   string
}

// DroppedOutputEvent is sent to notify a client that events have been dropped.
type DroppedOutputEvent struct {
	GerritEventBase
}

// HashtagsChangedEvent is sent when the hashtags have been added to or removed from a change.
type HashtagsChangedEvent struct {
	GerritEventBase
	Change   GerritChange
	Editor   GerritUser
	Added    []string
	Removed  []string
	Hashtags []string
}

// ProjectCreatedEvent is sent when a new project has been created.
type ProjectCreatedEvent struct {
	GerritEventBase
	ProjectName string
	ProjectHead string
}

// PatchSetCreatedEvent is sent when a new change has been uploaded, or a new patchset has been
// uploaded to an existing change.
type PatchSetCreatedEvent struct {
	GerritEventBase
	Change   GerritChange
	PatchSet GerritPatchSet
	Uploader GerritUser
}

// RefUpdatedEvent is sent when a reference is updated in a git repository.
type RefUpdatedEvent struct {
	GerritEventBase
	Submitter GerritUser
	RefUpdate struct {
		// OldRev is the old value of the ref, prior to the update.
		OldRev string
		// NewRev is the new value the ref was updated to.
		NewRev string
		// RefName is the full ref name within project.
		RefName string
		// Project is the project path within Gerrit.
		Project string
	}
}

// ReviewerAddedEvent is sent when a reviewer is added to a change.
type ReviewerAddedEvent struct {
	GerritEventBase
	Change   GerritChange
	PatchSet GerritPatchSet
	Reviewer GerritUser
}

// ReviewerDeletedEvent is sent when a reviewer (with a vote) is removed from a change.
type ReviewerDeletedEvent struct {
	GerritEventBase
	Change    GerritChange
	PatchSet  GerritPatchSet
	Reviewer  GerritUser
	Remover   GerritUser
	Approvals []GerritApproval
	Comment   string
}

// TopicChangedEvent is sent when the topic of a change has been changed.
type TopicChangedEvent struct {
	GerritEventBase
	Change   GerritChange
	Changer  GerritUser
	OldTopic string
}

// VoteDeletedEvent is sent when a vote was removed from a change.
type VoteDeletedEvent struct {
	GerritEventBase
	Change    GerritChange
	PatchSet  GerritPatchSet
	Reviewer  GerritUser
	Remover   GerritUser
	Approvals []GerritApproval
	Comment   string
}
