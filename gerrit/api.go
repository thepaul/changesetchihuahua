package gerrit

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/zeebo/errs"
)

// Client abstracts interaction with the Gerrit client, largely just so that it can be
// possible to mock. Ew.
type Client interface {
	QueryChangesEx(context.Context, []string, *QueryChangesOpts) ([]ChangeInfo, bool, error)
	GetChangeEx(context.Context, string, *QueryChangesOpts) (ChangeInfo, error)
	ListRevisionComments(context.Context, string, string) (map[string][]CommentInfo, error)
	GetPatchSetInfo(context.Context, string, string) (ChangeInfo, error)
	GetChangeReviewers(context.Context, string) ([]ReviewerInfo, error)
	QueryAccountsEx(context.Context, string, *QueryAccountsOpts) ([]AccountInfo, bool, error)
	URLForChange(*ChangeInfo) string
	Close() error
}

type client struct {
	ServerURL     *url.URL
	httpClient    *http.Client
	gerritVersion string
}

func OpenClient(ctx context.Context, serverURL string) (Client, error) {
	urlObj, err := url.Parse(serverURL)
	if err != nil {
		return nil, err
	}
	urlObj.Path = strings.TrimRight(urlObj.Path, "/") + "/"
	if urlObj.RawPath == "" {
		urlObj.RawPath = urlObj.Path
	} else {
		urlObj.RawPath = strings.TrimRight(urlObj.RawPath, "/") + "/"
	}

	client := &client{
		ServerURL:  urlObj,
		httpClient: http.DefaultClient,
	}
	client.gerritVersion, err = client.doGetString(ctx, "/config/server/version", nil)
	if err != nil {
		return nil, err
	}
	return client, nil
}

func (c *client) Close() error {
	c.httpClient.CloseIdleConnections()
	return nil
}

func (c *client) URLForChange(change *ChangeInfo) string {
	return c.makeURL(fmt.Sprintf("/c/%s/+/%d", url.PathEscape(change.Project), change.Number), nil)
}

func (c *client) makeURL(path string, query url.Values) string {
	path = strings.TrimLeft(path, "/")
	myURL := *c.ServerURL // copy
	myURL.RawPath += path
	decodedPath, err := url.PathUnescape(myURL.RawPath)
	if err != nil {
		// best effort
		myURL.Path += path
	} else {
		myURL.Path = decodedPath
	}
	if query != nil {
		if myURL.RawQuery == "" {
			myURL.RawQuery = query.Encode()
		} else {
			myURL.RawQuery += "&" + query.Encode()
		}
	}
	return myURL.String()
}

func (c *client) doGet(ctx context.Context, path string, query url.Values) (*http.Response, error) {
	myURL := c.makeURL(path, query)
	req, err := http.NewRequestWithContext(ctx, "GET", myURL, nil)
	if err != nil {
		return nil, errs.Wrap(err)
	}
	//req.Header.Add("accept-encoding", "gzip")
	req.Header.Add("accept", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, errs.New("could not query Gerrit: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, errs.New("unexpected status code %d from query to %q: %s", resp.StatusCode, myURL, resp.Status)
	}
	resp.Body = newGerritMagicRemovingReader(resp.Body)
	return resp, nil
}

func (c *client) doGetString(ctx context.Context, path string, query url.Values) (string, error) {
	resp, err := c.doGet(ctx, path, query)
	if err != nil {
		return "", err
	}
	defer func() { err = errs.Combine(err, resp.Body.Close()) }()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", errs.New("reading response body: %v", err)
	}
	return string(body), nil
}

func (c *client) QueryChangesEx(ctx context.Context, queries []string, opts *QueryChangesOpts) (changes []ChangeInfo, more bool, err error) {
	values := url.Values{
		"q": queries,
	}
	if labels := opts.assembleLabels(); len(labels) > 0 {
		values["o"] = labels
	}
	if opts.Limit > 0 {
		values.Set("n", strconv.Itoa(opts.Limit))
	}
	if opts.StartAt != 0 {
		values.Set("S", strconv.Itoa(opts.StartAt))
	}
	resp, err := c.doGet(ctx, "/changes/", values)
	if err != nil {
		return nil, false, errs.Wrap(err)
	}
	defer func() { err = errs.Combine(err, resp.Body.Close()) }()

	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(&changes); err != nil {
		return nil, false, errs.Wrap(err)
	}

	if len(changes) > 0 && changes[len(changes)-1].MoreChanges {
		more = true
	}
	return changes, more, nil
}

func (c *client) QueryChanges(ctx context.Context, query string) ([]ChangeInfo, error) {
	changes, _, err := c.QueryChangesEx(ctx, []string{query}, &QueryChangesOpts{})
	return changes, err
}

func (c *client) GetChangeEx(ctx context.Context, changeID string, opts *QueryChangesOpts) (changeInfo ChangeInfo, err error) {
	values := url.Values{}
	if labels := opts.assembleLabels(); len(labels) > 0 {
		values["o"] = labels
	}
	resp, err := c.doGet(ctx, "/changes/"+url.PathEscape(changeID)+"/", values)
	if err != nil {
		return changeInfo, err
	}
	defer func() { err = errs.Combine(err, resp.Body.Close()) }()

	decoder := json.NewDecoder(resp.Body)
	err = decoder.Decode(&changeInfo)
	return changeInfo, err
}

func (c *client) GetChangeReviewers(ctx context.Context, changeID string) ([]ReviewerInfo, error) {
	resp, err := c.doGet(ctx, "/changes/"+url.PathEscape(changeID)+"/reviewers/", nil)
	if err != nil {
		return nil, err
	}
	defer func() { err = errs.Combine(err, resp.Body.Close()) }()

	decoder := json.NewDecoder(resp.Body)
	var reviewers []ReviewerInfo
	err = decoder.Decode(&reviewers)
	return reviewers, err
}

func (c *client) GetPatchSetInfo(ctx context.Context, changeID, patchSetID string) (changeInfo ChangeInfo, err error) {
	queryPath := fmt.Sprintf("/changes/%s/revisions/%s/review",
		url.PathEscape(changeID), url.PathEscape(patchSetID))
	resp, err := c.doGet(ctx, queryPath, nil)
	if err != nil {
		return changeInfo, err
	}
	defer func() { err = errs.Combine(err, resp.Body.Close()) }()

	decoder := json.NewDecoder(resp.Body)
	err = decoder.Decode(&changeInfo)
	return changeInfo, err
}

func (c *client) ListChangeMessages(ctx context.Context, changeID string) ([]ChangeMessageInfo, error) {
	resp, err := c.doGet(ctx, fmt.Sprintf("/changes/%s/messages", url.PathEscape(changeID)), nil)
	if err != nil {
		return nil, err
	}
	defer func() { err = errs.Combine(err, resp.Body.Close()) }()

	var changeMessages []ChangeMessageInfo
	decoder := json.NewDecoder(resp.Body)
	err = decoder.Decode(&changeMessages)
	return changeMessages, err
}

func (c *client) ListRevisionComments(ctx context.Context, changeID, revisionID string) (map[string][]CommentInfo, error) {
	resp, err := c.doGet(ctx, fmt.Sprintf("/changes/%s/revisions/%s/comments/", url.PathEscape(changeID), url.PathEscape(revisionID)), nil)
	if err != nil {
		return nil, err
	}
	defer func() { err = errs.Combine(err, resp.Body.Close()) }()

	var commentMap map[string][]CommentInfo
	decoder := json.NewDecoder(resp.Body)
	err = decoder.Decode(&commentMap)
	return commentMap, err
}

// QueryChangesOpts controls behavior of queries to change-related API endpoints.
type QueryChangesOpts struct {
	// Limit specifies a limit on the number of results returned from a QueryChangesEx call.
	Limit int
	// StartAt specifies a number of changes to skip when querying multiple. This and Limit can
	// be combined to implement paging.
	StartAt int
	// DescribeLabels requests inclusion of a summary of each label required for submit, and
	// approvers that have granted (or rejected) that label.
	DescribeLabels bool
	// DescribeDetailedLabels requests inclusion of detailed label information, including
	// numeric values of all existing approvals, recognized label values, values permitted to
	// be set by the current user, all reviewers by state, and reviewers that may be removed by
	// the current user.
	DescribeDetailedLabels bool
	// DescribeCurrentRevision requests inclusion of details about the current revision (patch
	// set) of the change, including the commit SHA-1 and URLs to fetch from.
	DescribeCurrentRevision bool
	// DescribeAllRevisions requests inclusion of details about all revisions, not just
	// current.
	DescribeAllRevisions bool
	// DescribeDownloadCommands requests inclusion of the commands field in the FetchInfo for
	// revisions. Only valid when the DescribeCurrentRevision or DescribeAllRevisions options
	// are selected.
	DescribeDownloadCommands bool
	// DescribeCurrentCommit requests inclusion of all header fields from the commit object,
	// including message. Only valid when the DescribeCurrentRevision or DescribeAllRevisions
	// options are selected.
	DescribeCurrentCommit bool
	// DescribeAllCommits requests inclusion of all header fields from the output revisions.
	// If only DescribeCurrentRevision was requested then only the current revision’s commit
	// data will be output.
	DescribeAllCommits bool
	// DescribeCurrentFiles requests inclusion of list files modified by the commit and magic
	// files, including basic line counts inserted/deleted per file. Only valid when the
	// DescribeCurrentRevision or DescribeAllRevisions options are selected.
	DescribeCurrentFiles bool
	// DescribeAllFiles requests inclusion of all files modified by the commit and magic files,
	// including basic line counts inserted/deleted per file. If only DescribeCurrentRevision
	// was requested then only that commit’s modified files will be output.
	DescribeAllFiles bool
	// DescribeDetailedAccounts requests inclusion of the AccountID, Email and Username fields
	// in AccountInfo entities.
	DescribeDetailedAccounts bool
	// DescribeReviewerUpdates requests inclusion of updates to reviewers set as
	// ReviewerUpdateInfo entities.
	DescribeReviewerUpdates bool
	// DescribeMessages requests inclusion of messages associated with the change.
	DescribeMessages bool
	// DescribeCurrentActions requests inclusion of include information on available actions
	// for the change and its current revision. Ignored if the caller is not authenticated.
	DescribeCurrentActions bool
	// DescribeChangeActions requests inclusion of information on available change actions for
	// the change. Ignored if the caller is not authenticated.
	DescribeChangeActions bool
	// DescribeReviewed requests inclusion of the reviewed field if all of the following are
	// true: (1) the change is open, (2) the caller is authenticated, and (3) the caller has
	// commented on the change more recently than the last update from the change owner, i.e.
	// this change would show up in the results of reviewedby:self.
	DescribeReviewed bool
	// DescribeSkipMergeable requests skipping of the Mergeable field in ChangeInfo. For fast-
	// moving projects, this field must be recomputed often, which is slow for projects with
	// big trees.
	//
	// When change.api.excludeMergeableInChangeInfo is set in the gerrit.config, the mergeable
	// field will always be omitted and DescribeSkipMergeable has no effect.
	//
	// A change’s mergeability can be requested separately by calling the get-mergeable
	// endpoint.
	DescribeSkipMergeable bool
	// DescribeSubmittable requests inclusion of the Submittable field in ChangeInfo entities,
	// which can be used to tell if the change is reviewed and ready for submit.
	DescribeSubmittable bool
	// DescribeWebLinks requests inclusion of the WebLinks field in CommitInfo entities,
	// therefore only valid in combination with DescribeCurrentCommit or DescribeAllCommits.
	DescribeWebLinks bool
	// DescribeCheck requests inclusion of potential problems with the change.
	DescribeCheck bool
	// DescribeCommitFooters requests inclusion of the full commit message with Gerrit-specific
	// commit footers in the RevisionInfo.
	DescribeCommitFooters bool
	// DescribePushCertificates requests inclusion of push certificate information in the
	// RevisionInfo. Ignored if signed push is not enabled on the server.
	DescribePushCertificates bool
	// DescribeTrackingIDs requests inclusion of references to external tracking systems as
	// TrackingIDInfo entities.
	DescribeTrackingIDs bool
	// DescribeNoLimit requests all results to be returned.
	DescribeNoLimit bool
}

func (o *QueryChangesOpts) assembleLabels() (labels []string) {
	if o.DescribeLabels {
		labels = append(labels, "LABELS")
	}
	if o.DescribeDetailedLabels {
		labels = append(labels, "DETAILED_LABELS")
	}
	if o.DescribeCurrentRevision {
		labels = append(labels, "CURRENT_REVISION")
	}
	if o.DescribeAllRevisions {
		labels = append(labels, "ALL_REVISIONS")
	}
	if o.DescribeDownloadCommands {
		labels = append(labels, "DOWNLOAD_COMMANDS")
	}
	if o.DescribeCurrentCommit {
		labels = append(labels, "CURRENT_COMMIT")
	}
	if o.DescribeAllCommits {
		labels = append(labels, "ALL_COMMITS")
	}
	if o.DescribeCurrentFiles {
		labels = append(labels, "CURRENT_FILES")
	}
	if o.DescribeAllFiles {
		labels = append(labels, "ALL_FILES")
	}
	if o.DescribeDetailedAccounts {
		labels = append(labels, "DETAILED_ACCOUNTS")
	}
	if o.DescribeReviewerUpdates {
		labels = append(labels, "REVIEWER_UPDATES")
	}
	if o.DescribeMessages {
		labels = append(labels, "MESSAGES")
	}
	if o.DescribeCurrentActions {
		labels = append(labels, "CURRENT_ACTIONS")
	}
	if o.DescribeChangeActions {
		labels = append(labels, "CHANGE_ACTIONS")
	}
	if o.DescribeReviewed {
		labels = append(labels, "REVIEWED")
	}
	if o.DescribeSkipMergeable {
		labels = append(labels, "SKIP_MERGEABLE")
	}
	if o.DescribeSubmittable {
		labels = append(labels, "SUBMITTABLE")
	}
	if o.DescribeWebLinks {
		labels = append(labels, "WEB_LINKS")
	}
	if o.DescribeCheck {
		labels = append(labels, "CHECK")
	}
	if o.DescribeCommitFooters {
		labels = append(labels, "COMMIT_FOOTERS")
	}
	if o.DescribePushCertificates {
		labels = append(labels, "PUSH_CERTIFICATES")
	}
	if o.DescribeTrackingIDs {
		labels = append(labels, "TRACKING_IDS")
	}
	if o.DescribeNoLimit {
		labels = append(labels, "NO-LIMIT")
	}
	return labels
}

func (c *client) QueryAccountsEx(ctx context.Context, query string, opts *QueryAccountsOpts) (users []AccountInfo, more bool, err error) {
	values := url.Values{
		"q": []string{query},
	}
	if labels := opts.assembleLabels(); len(labels) > 0 {
		values["o"] = labels
	}
	if opts.Limit > 0 {
		values.Set("n", strconv.Itoa(opts.Limit))
	}
	if opts.StartAt != 0 {
		values.Set("S", strconv.Itoa(opts.StartAt))
	}
	resp, err := c.doGet(ctx, "/accounts/", values)
	if err != nil {
		return nil, false, errs.Wrap(err)
	}
	defer func() { err = errs.Combine(err, resp.Body.Close()) }()

	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(&users); err != nil {
		return nil, false, errs.Wrap(err)
	}

	if len(users) > 0 && users[len(users)-1].MoreAccounts {
		more = true
	}
	return users, more, nil
}

func (c *client) QueryAccounts(ctx context.Context, query string) ([]AccountInfo, error) {
	changes, _, err := c.QueryAccountsEx(ctx, query, &QueryAccountsOpts{})
	return changes, err
}

type QueryAccountsOpts struct {
	Limit             int
	StartAt           int
	DescribeDetails   bool
	DescribeAllEmails bool
}

func (o *QueryAccountsOpts) assembleLabels() (labels []string) {
	if o.DescribeDetails {
		labels = append(labels, "DETAILS")
	}
	if o.DescribeAllEmails {
		labels = append(labels, "ALL_EMAILS")
	}
	return labels
}

const gerritMagic = ")]}'\n"

func newGerritMagicRemovingReader(underlying io.ReadCloser) io.ReadCloser {
	pipeReader, pipeWriter := io.Pipe()
	go func() {
		myBuf := make([]byte, len(gerritMagic))
		n, err := io.ReadFull(underlying, myBuf)
		if err != nil {
			_, _ = pipeWriter.Write(myBuf[:n])
			_ = underlying.Close()
			_ = pipeWriter.CloseWithError(err)
			return
		}
		if string(myBuf) != gerritMagic {
			_, _ = pipeWriter.Write(myBuf)
		}
		_, err = io.Copy(pipeWriter, underlying)
		_ = underlying.Close()
		_ = pipeWriter.CloseWithError(err)
	}()
	return pipeReader
}
