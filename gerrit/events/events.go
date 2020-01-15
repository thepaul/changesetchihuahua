package events

import (
	"encoding/json"
	"time"

	"github.com/zeebo/errs"
)

var (
	EventDecodingError = errs.Class("event decoding error")
)

// MaxEventPayloadSize is the size of the largest event payload this system will allow. Larger
// event payloads will result in errors and will not be processed.
const MaxEventPayloadSize = 10 * 1024 * 1024

// GerritEvent is a common interface to various gerrit event structures.
type GerritEvent interface {
	GetType() string
	EventCreatedAt() time.Time
}

// Base is a base for gerrit event types, providing the necessary methods for
// GerritEvent compatibility.
type Base struct {
	Type           string
	EventCreatedOn int64 `json:"eventCreatedOn"`
}

func (g *Base) GetType() string {
	return g.Type
}

func (g *Base) EventCreatedAt() time.Time {
	return UnixInt64Time(g.EventCreatedOn)
}

func DecodeGerritEvent(eventJSON []byte) (GerritEvent, error) {
	var eventType Base
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
	case "wip-state-changed":
		evStruct = &WipStateChangedEvent{}
	default:
		return nil, EventDecodingError.New("unrecognized event type %q", eventType.Type)
	}
	if err := json.Unmarshal(eventJSON, evStruct); err != nil {
		return nil, EventDecodingError.Wrap(err)
	}
	return evStruct.(GerritEvent), nil
}

// AssigneeChangedEvent is sent when the assignee of a change has been modified.
type AssigneeChangedEvent struct {
	Base
	Change      Change
	Changer     Account
	OldAssignee Account
}

// ChangeAbandonedEvent is sent when a change has been abandoned.
type ChangeAbandonedEvent struct {
	Base
	Change    Change
	PatchSet  PatchSet
	Abandoner Account
	Reason    string
}

// ChangeMergedEvent is sent when a change has been merged into the git repository.
type ChangeMergedEvent struct {
	Base
	Change    Change
	PatchSet  PatchSet
	Submitter Account
	NewRev    string
}

// ChangeRestoredEvent is sent when an abandoned change has been restored.
type ChangeRestoredEvent struct {
	Base
	Change   Change
	PatchSet PatchSet
	Restorer Account
	Reason   string
}

// CommentAddedEvent is sent when a review comment has been posted on a change.
type CommentAddedEvent struct {
	Base
	Change    Change
	PatchSet  PatchSet
	Author    Account
	Approvals []Approval
	Comment   string
}

// DroppedOutputEvent is sent to notify a client that events have been dropped.
type DroppedOutputEvent struct {
	Base
}

// HashtagsChangedEvent is sent when the hashtags have been added to or removed from a change.
type HashtagsChangedEvent struct {
	Base
	Change   Change
	Editor   Account
	Added    []string
	Removed  []string
	Hashtags []string
}

// ProjectCreatedEvent is sent when a new project has been created.
type ProjectCreatedEvent struct {
	Base
	ProjectName string
	ProjectHead string
}

// PatchSetCreatedEvent is sent when a new change has been uploaded, or a new patchset has been
// uploaded to an existing change.
type PatchSetCreatedEvent struct {
	Base
	Change   Change
	PatchSet PatchSet
	Uploader Account
}

// RefUpdatedEvent is sent when a reference is updated in a git repository.
type RefUpdatedEvent struct {
	Base
	Submitter Account
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
	Base
	Change   Change
	PatchSet PatchSet
	Reviewer Account
}

// ReviewerDeletedEvent is sent when a reviewer (with a vote) is removed from a change.
type ReviewerDeletedEvent struct {
	Base
	Change    Change
	PatchSet  PatchSet
	Reviewer  Account
	Remover   Account
	Approvals []Approval
	Comment   string
}

// TopicChangedEvent is sent when the topic of a change has been changed.
type TopicChangedEvent struct {
	Base
	Change   Change
	Changer  Account
	OldTopic string
}

// VoteDeletedEvent is sent when a vote was removed from a change.
type VoteDeletedEvent struct {
	Base
	Change    Change
	PatchSet  PatchSet
	Reviewer  Account
	Remover   Account
	Approvals []Approval
	Comment   string
}

// WipStateChangedEvent is sent when the wip state is changed on a changeset.
type WipStateChangedEvent struct {
	Base
	Changer  Account
	PatchSet PatchSet
	Change   Change
	RefName  string
}
