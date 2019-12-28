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

type Client struct {
	ServerURL     *url.URL
	httpClient    *http.Client
	gerritVersion string
}

func OpenClient(ctx context.Context, serverURL string) (*Client, error) {
	urlObj, err := url.Parse(serverURL)
	if err != nil {
		return nil, err
	}
	if !strings.HasSuffix(urlObj.Path, "/") {
		urlObj.Path = urlObj.Path + "/"
	}

	client := &Client{
		ServerURL:  urlObj,
		httpClient: http.DefaultClient,
	}
	client.gerritVersion, err = client.doGetString(ctx, "/config/server/version", nil)
	if err != nil {
		return nil, err
	}
	return client, nil
}

func (c *Client) Close() error {
	c.httpClient.CloseIdleConnections()
	return nil
}

func (c *Client) URLForChange(change *ChangeInfo) string {
	return c.makeURL(fmt.Sprintf("c/%s/+/%d", change.Project, change.Number), nil)
}

func (c *Client) makeURL(path string, query url.Values) string {
	myURL := *c.ServerURL // copy
	myURL.Path += path
	if query != nil {
		if myURL.RawQuery == "" {
			myURL.RawQuery = query.Encode()
		} else {
			myURL.RawQuery += "&" + query.Encode()
		}
	}
	return myURL.String()
}

func (c *Client) doGet(ctx context.Context, path string, query url.Values) (*http.Response, error) {
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

func (c *Client) doGetString(ctx context.Context, path string, query url.Values) (string, error) {
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

func (c *Client) QueryChangesEx(ctx context.Context, queries []string, opts *QueryChangesOpts) (changes []ChangeInfo, more bool, err error) {
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
	resp, err := c.doGet(ctx, "changes/", values)
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

func (c *Client) QueryChanges(ctx context.Context, query string) ([]ChangeInfo, error) {
	changes, _, err := c.QueryChangesEx(ctx, []string{query}, &QueryChangesOpts{})
	return changes, err
}

type QueryChangesOpts struct {
	Limit                    int
	StartAt                  int
	DescribeLabels           bool
	DescribeDetailedLabels   bool
	DescribeCurrentRevision  bool
	DescribeAllRevisions     bool
	DescribeDownloadCommands bool
	DescribeCurrentCommit    bool
	DescribeAllCommits       bool
	DescribeCurrentFiles     bool
	DescribeAllFiles         bool
	DescribeDetailedAccounts bool
	DescribeReviewerUpdates  bool
	DescribeMessages         bool
	DescribeCurrentActions   bool
	DescribeChangeActions    bool
	DescribeReviewed         bool
	DescribeSkipMergeable    bool
	DescribeSubmittable      bool
	DescribeWebLinks         bool
	DescribeCheck            bool
	DescribeCommitFooters    bool
	DescribePushCertificates bool
	DescribeTrackingIDs      bool
	DescribeNoLimit          bool
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

func (c *Client) QueryAccountsEx(ctx context.Context, query string, opts *QueryAccountsOpts) (users []AccountInfo, more bool, err error) {
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

func (c *Client) QueryAccounts(ctx context.Context, query string) ([]AccountInfo, error) {
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
