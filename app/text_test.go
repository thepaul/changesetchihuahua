package app

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStringTransformersRealishUseCase(t *testing.T) {
	transformer := `s,^https?://([^ ?]+)/job/([^/?]+)/([0-9]+)/,https://$1/blue/organizations/jenkins/$2/detail/$2/$3/pipeline/,`
	s, err := applyStringTransformer("https://build.dev.jorts.io/job/jorts-gerrit/18378/", transformer)
	require.NoError(t, err)
	assert.Equal(t, "https://build.dev.jorts.io/blue/organizations/jenkins/jorts-gerrit/detail/jorts-gerrit/18378/pipeline/", s)
}

func TestStringTransformerSubstEmptyMatch(t *testing.T) {
	transformer := `s//a/`
	s, err := applyStringTransformer("bcdefg", transformer)
	require.NoError(t, err)
	assert.Equal(t, "abcdefg", s)
}

func TestStringTransformerSubstAllEmptyMatch(t *testing.T) {
	transformer := `s//a/g`
	s, err := applyStringTransformer("bcdefg", transformer)
	require.NoError(t, err)
	assert.Equal(t, "abacadaeafaga", s)
}

func TestStringTransformerUnknown(t *testing.T) {
	transformer := `x/y`
	s, err := applyStringTransformer("foobar", transformer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid transformer")
	assert.Equal(t, "", s)
}

func TestStringTransformerNoMatch(t *testing.T) {
	transformer := `s/abc/def/`
	s, err := applyStringTransformer("fish and chips", transformer)
	require.NoError(t, err)
	assert.Equal(t, "fish and chips", s)
}

func TestStringTransformerEmptySubject(t *testing.T) {
	transformer := `s/a(.)c/$1/`
	s, err := applyStringTransformer("", transformer)
	require.NoError(t, err)
	assert.Equal(t, "", s)
}

func TestStringTransformerInvalidRegexps(t *testing.T) {
	transformer := `s/a++/b/g`
	s, err := applyStringTransformer("aaa+/", transformer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bad regex")
	assert.Equal(t, "", s)
}

func TestStringTransformerDeleteAll(t *testing.T) {
	transformer := `s/foo//g`
	s, err := applyStringTransformer("foobfoobarfoofoobazfoo", transformer)
	require.NoError(t, err)
	assert.Equal(t, "bbarbaz", s)
}

func TestStringTransformerReplaceWithGroups(t *testing.T) {
	transformer := `s!<(.)>!<$1></$1>!g`
	s, err := applyStringTransformer("<b><c>ok<cb><b>", transformer)
	require.NoError(t, err)
	assert.Equal(t, "<b></b><c></c>ok<cb><b></b>", s)
}
