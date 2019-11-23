package events

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	testJSON1 = `{"submitter":{"name":"paul cannon","username":"thepaul"},"refUpdate":{"oldRev":"71763f6714a0801419e0ade3717e1c4c88c71525","newRev":"961c54eb3928de646246bd953936b4e8fcf2d77c","refName":"refs/changes/19/119/meta","project":"storj/storj"},"type":"ref-updated","eventCreatedOn":1574210563}`
	testJSON2 = `{"author":{"name":"paul cannon","username":"thepaul"},"approvals":[{"type":"Verified","description":"Verified","value":"0"},{"type":"Code-Review","description":"Code-Review","value":"0"}],"comment":"Patch Set 14:\n\n(1 comment)","patchSet":{"number":14,"revision":"173566d50845742e7432f2ca72ea336d75c54466","parents":["8b3444e0883fbdd961f8f4a3b2b6432d83e53ea4"],"ref":"refs/changes/19/119/14","uploader":{"name":"paul cannon","username":"thepaul"},"createdOn":1573925853,"author":{"name":"paul cannon","email":"paul@thepaul.org","username":""},"kind":"TRIVIAL_REBASE","sizeInsertions":124,"sizeDeletions":-88},"change":{"project":"storj/storj","branch":"master","id":"I171c7a3818beffbad969b131e98b9bbe3f324bf2","number":119,"subject":"storage/testsuite: pass ctx in to bulk setup methods","owner":{"name":"paul cannon","username":"thepaul"},"url":"https://review.dev.storj.io/c/storj/storj/+/119","commitMessage":"storage/testsuite: pass ctx in to bulk setup methods\n\nto make them cancelable.\n\nChange-Id: I171c7a3818beffbad969b131e98b9bbe3f324bf2\n","createdOn":1571975987,"status":"NEW"},"project":"storj/storj","refName":"refs/heads/master","changeKey":{"id":"I171c7a3818beffbad969b131e98b9bbe3f324bf2"},"type":"comment-added","eventCreatedOn":1574210563}`
)

func TestGerritChangeDecode(t *testing.T) {
	ev, err := DecodeGerritEvent([]byte(testJSON1))
	require.NoError(t, err, "failed to decode testJSON1")
	require.IsType(t, &RefUpdatedEvent{}, ev)
	refEv := ev.(*RefUpdatedEvent)
	require.Equal(t, "ref-updated", refEv.GetType())
	require.Equal(t,
		time.Date(2019, 11, 20, 0, 42, 43, 0, time.UTC),
		refEv.EventCreatedAt().UTC())
	require.Equal(t, "thepaul", refEv.Submitter.Username)

	ev, err = DecodeGerritEvent([]byte(testJSON2))
	require.NoError(t, err, "failed to decode testJSON2")
	require.IsType(t, &CommentAddedEvent{}, ev)
	commentEv := ev.(*CommentAddedEvent)
	require.Equal(t, "comment-added", commentEv.GetType())
	require.Equal(t,
		time.Date(2019, 11, 20, 0, 42, 43, 0, time.UTC),
		commentEv.EventCreatedAt().UTC())
	require.Equal(t, "thepaul", commentEv.Author.Username)
	require.Equal(t, 2, len(commentEv.Approvals))
	require.Equal(t, "storage/testsuite: pass ctx in to bulk setup methods", commentEv.Change.Subject)
}
