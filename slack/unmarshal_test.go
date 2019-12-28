package slack

import (
	"encoding/json"
	"testing"

	"github.com/nlopes/slack"
)

// v0.6.0 of nlopes/slack can't unmarshal message events if they have 'rich_text' blocks
// in them, as this test string does. it's important because apparently all messages have
// them now! maybe this only happens for newer apps? unclear.
func TestSlackLibCanUnmarshalBlocksWithRichText(t *testing.T) {
	const eventJSON = "{\"blocks\":[{\"type\":\"rich_text\",\"block_id\":\"5g6vY\",\"elements\":[{\"type\":\"rich_text_section\",\"elements\":[{\"type\":\"text\",\"text\":\"jfdjfkdl\"}]}]}]}"

	var x slack.Msg
	if err := json.Unmarshal([]byte(eventJSON), &x); err != nil {
		t.Fatalf("could not unmarshal: %v", err)
	}
	t.Log("all good")
}
