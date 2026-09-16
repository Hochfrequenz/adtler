package adt

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/Hochfrequenz/adtler/adt/adtxml"
)

func (c *httpClient) CheckTransport(ctx context.Context, pgmID, object, objectName string) (*TransportCheckResult, error) {
	reqData := adtxml.TransportCheckRequest{
		PgmID:      strings.ToUpper(pgmID),
		Object:     strings.ToUpper(object),
		ObjectName: strings.ToUpper(objectName),
		Operation:  "I",
	}
	body, err := adtxml.MarshalASXData(reqData)
	if err != nil {
		return nil, fmt.Errorf("CheckTransport marshal: %w", err)
	}

	resp, err := c.doMutate(ctx, http.MethodPost,
		"/sap/bc/adt/cts/transportchecks",
		strings.NewReader(string(body)),
		map[string]string{
			"Content-Type": "application/vnd.sap.as+xml; charset=utf-8; dataname=com.sap.adt.transport.CheckObjects",
			"Accept":       "application/vnd.sap.as+xml",
		},
	)
	if err != nil {
		return nil, fmt.Errorf("CheckTransport: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkResponse(resp); err != nil {
		return nil, err
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("CheckTransport reading body: %w", err)
	}

	checkData, err := adtxml.UnmarshalASXData[adtxml.TransportCheckData](data)
	if err != nil {
		return nil, fmt.Errorf("CheckTransport parsing: %w", err)
	}

	result := &TransportCheckResult{
		PgmID:      checkData.PgmID,
		Object:     checkData.Object,
		ObjectName: checkData.ObjectName,
		DevClass:   checkData.DevClass,
		Result:     checkData.Result,
		Recording:  checkData.Recording == "X",
	}
	for _, req := range checkData.Requests {
		result.Requests = append(result.Requests, TransportRequest{
			Number:      req.Header.TrKorr,
			Description: req.Header.Text,
			Status:      req.Header.TrStatus,
		})
	}
	return result, nil
}

func (c *httpClient) CreateTransport(ctx context.Context, category, target, description, devClass string) (string, error) {
	reqData := adtxml.CreateTransportData{
		Category:    strings.ToUpper(category),
		Target:      strings.ToUpper(target),
		Text:        description,
		Description: description,
		DevClass:    strings.ToUpper(devClass),
	}
	body, err := adtxml.MarshalASXData(reqData)
	if err != nil {
		return "", fmt.Errorf("CreateTransport marshal: %w", err)
	}

	resp, err := c.doMutate(ctx, http.MethodPost,
		"/sap/bc/adt/cts/transports",
		strings.NewReader(string(body)),
		map[string]string{
			// The Content-Type controls the response format: v1 returns plain text,
			// dataname=...CreateCorrectionRequest.v1 returns ASX XML with TRKORR.
			"Content-Type": "application/vnd.sap.as+xml; charset=UTF-8; dataname=com.sap.adt.CreateCorrectionRequest.v1",
			"Accept":       "application/vnd.sap.as+xml",
		},
	)
	if err != nil {
		return "", fmt.Errorf("CreateTransport: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkResponse(resp); err != nil {
		return "", err
	}

	data, _ := io.ReadAll(resp.Body)
	if len(data) > 0 {
		asxData, err := adtxml.UnmarshalASXData[struct {
			TrKorr string `xml:"TRKORR"`
		}](data)
		if err == nil && asxData.TrKorr != "" {
			return asxData.TrKorr, nil
		}
	}

	return "", fmt.Errorf("CreateTransport: transport created but number not returned — check GetTransportRequests to find it")
}

func (c *httpClient) CreateTransportTask(ctx context.Context, parentTransport, owner, description string) (string, error) {
	var descBuf strings.Builder
	_ = xml.EscapeText(&descBuf, []byte(description))
	if owner == "" {
		owner = c.cfg.User
	}
	owner = strings.ToUpper(owner)
	body := `<?xml version="1.0" encoding="utf-8"?>` +
		`<tm:root xmlns:tm="http://www.sap.com/cts/adt/tm"` +
		` tm:useraction="newtask" tm:targetuser="` + owner + `">` +
		`<tm:task tm:owner="` + owner + `" tm:desc="` + descBuf.String() + `"/>` +
		`</tm:root>`

	path := "/sap/bc/adt/cts/transportrequests/" + parentTransport + "/tasks"
	resp, err := c.doMutate(ctx, http.MethodPost, path,
		strings.NewReader(body),
		map[string]string{
			"Content-Type": "application/vnd.sap.adt.transportorganizer.v1+xml",
			"Accept":       "application/vnd.sap.adt.transportorganizer.v1+xml",
		},
	)
	if err != nil {
		return "", fmt.Errorf("CreateTransportTask: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkResponse(resp); err != nil {
		return "", err
	}

	data, _ := io.ReadAll(resp.Body)
	if len(data) > 0 {
		// The response has the task number as tm:number attribute on tm:root.
		var taskRoot struct {
			Number string `xml:"number,attr"`
		}
		if err := xml.Unmarshal(data, &taskRoot); err == nil && taskRoot.Number != "" {
			return taskRoot.Number, nil
		}
	}

	return "", fmt.Errorf("CreateTransportTask: task created but number not returned")
}

func (c *httpClient) DeleteTransport(ctx context.Context, transportNumber string) error {
	path := "/sap/bc/adt/cts/transportrequests/" + transportNumber
	resp, err := c.doMutate(ctx, http.MethodDelete, path, nil, nil)
	if err != nil {
		return fmt.Errorf("DeleteTransport: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	return checkResponse(resp)
}

// ReleaseTransport releases a transport request or task.
// If the request has unreleased tasks, it returns an error listing them.
func (c *httpClient) ReleaseTransport(ctx context.Context, transportNumber string) error {
	return c.releaseTransport(ctx, transportNumber, false)
}

// ReleaseTransportWithTasks releases a transport request including all its tasks.
// Each task is released first, then the request itself.
func (c *httpClient) ReleaseTransportWithTasks(ctx context.Context, transportNumber string) error {
	return c.releaseTransport(ctx, transportNumber, true)
}

// ReleaseResult reports the outcome of a verified transport release.
type ReleaseResult struct {
	Transport string `json:"transport"`
	// Released is true when the request is confirmed no longer modifiable
	// after the release. It is false — with a nil error — when the release
	// endpoint reported success but the request stayed modifiable, the
	// "silent failure" some systems (notably ECC) exhibit.
	Released bool `json:"released"`
}

// ReleaseTransportVerified releases a transport request (optionally including
// its tasks) and then confirms the outcome by re-reading the request status.
//
// The plain ReleaseTransport reports success whenever the release endpoint
// returns 2xx and its release report carries no error — but some systems
// (notably ECC) return 200 while leaving the request modifiable. This method
// detects that by checking the post-release status: a request still in the
// modifiable ("D") state is reported as Released=false (with a nil error) so
// callers can fall back to another release mechanism. A genuine release error
// is returned unchanged.
//
// If the post-release status read fails, the release is assumed to have
// succeeded (Released=true) — the optimistic behavior callers had before
// verification existed.
func (c *httpClient) ReleaseTransportVerified(ctx context.Context, transportNumber string, includeTasks bool) (*ReleaseResult, error) {
	var err error
	if includeTasks {
		err = c.ReleaseTransportWithTasks(ctx, transportNumber)
	} else {
		err = c.ReleaseTransport(ctx, transportNumber)
	}
	if err != nil {
		return nil, err
	}

	if info, infoErr := c.GetTransportInfo(ctx, transportNumber); infoErr == nil && info != nil && info.Status == TransportStatusModifiable {
		return &ReleaseResult{Transport: transportNumber, Released: false}, nil
	}
	return &ReleaseResult{Transport: transportNumber, Released: true}, nil
}

func (c *httpClient) releaseTransport(ctx context.Context, transportNumber string, releaseTasks bool) error {
	err := c.releaseTransportDirect(ctx, transportNumber)
	if err == nil {
		return nil
	}

	// Check for unreleased tasks.
	tasks, taskErr := c.GetTransportTasks(ctx, transportNumber)
	if taskErr != nil || len(tasks) == 0 {
		return err
	}

	if !releaseTasks {
		return fmt.Errorf("transport %s has %d unreleased task(s): %s — release them first or use ReleaseTransportWithTasks",
			transportNumber, len(tasks), strings.Join(tasks, ", "))
	}

	for _, task := range tasks {
		if taskReleaseErr := c.releaseTransportDirect(ctx, task); taskReleaseErr != nil {
			return fmt.Errorf("ReleaseTransport: releasing task %s failed: %w", task, taskReleaseErr)
		}
	}
	return c.releaseTransportDirect(ctx, transportNumber)
}

// releaseTransportDirect releases a single transport or task.
// Tasks use /releasejobs (works on ECC and S4).
// Requests use /newreleasejobs (S4 only — ECC returns 400, see #224).
// Falls back to /releasejobs if /newreleasejobs is not available.
//
// The release may complete synchronously (response contains release report)
// or asynchronously (response contains a background run to poll).
func (c *httpClient) releaseTransportDirect(ctx context.Context, transportNumber string) error {
	headers := map[string]string{
		"Accept":                "application/vnd.sap.adt.transportorganizer.v1+xml",
		"X-sap-adt-sessiontype": "stateful",
	}

	// Try /newreleasejobs first (works for requests on S4).
	path := "/sap/bc/adt/cts/transportrequests/" + transportNumber + "/newreleasejobs"
	resp, err := c.doMutate(ctx, http.MethodPost, path, nil, headers)
	if err != nil {
		return fmt.Errorf("ReleaseTransport %s: %w", transportNumber, err)
	}
	data, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode < 400 {
		// Check if this is an async background run response.
		if pollURI := ParseBackgroundRunPollURI(data); pollURI != "" {
			return c.pollBackgroundRelease(ctx, transportNumber, pollURI)
		}
		return checkReleaseResponse(transportNumber, data)
	}

	// Fallback to /releasejobs (works for tasks on ECC and S4).
	path = "/sap/bc/adt/cts/transportrequests/" + transportNumber + "/releasejobs"
	resp, err = c.doMutate(ctx, http.MethodPost, path, nil, headers)
	if err != nil {
		return fmt.Errorf("ReleaseTransport %s: %w", transportNumber, err)
	}
	data, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err := checkResponse(resp); err != nil {
		return err
	}
	return checkReleaseResponse(transportNumber, data)
}

const backgroundRunPollInterval = 10 * time.Second

// ParseBackgroundRunPollURI checks if the response is a background run
// and returns the poll URI if so. Returns empty string for sync responses.
func ParseBackgroundRunPollURI(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	var bgRun struct {
		XMLName xml.Name `xml:"run"`
		Status  string   `xml:"status"`
		Links   []struct {
			Rel  string `xml:"rel,attr"`
			Href string `xml:"href,attr"`
		} `xml:"link"`
	}
	if err := xml.Unmarshal(data, &bgRun); err != nil {
		return ""
	}
	// A background run has a status like "running" or "new".
	if bgRun.Status == "" {
		return ""
	}
	// The poll URI is the run itself — return a marker so the caller knows.
	// Eclipse reads the Location header, but we can also self-link.
	for _, link := range bgRun.Links {
		if strings.Contains(link.Rel, "self") && link.Href != "" {
			return link.Href
		}
	}
	return ""
}

// pollBackgroundRelease polls a background release job until it completes.
// SAP background runs use 10-second minimum polling intervals.
func (c *httpClient) pollBackgroundRelease(ctx context.Context, transportNumber, pollURI string) error {
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("ReleaseTransport %s: %w", transportNumber, ctx.Err())
		case <-time.After(c.pollInterval):
		}

		resp, err := c.doRead(ctx, pollURI, map[string]string{
			"Accept": "application/vnd.sap.adt.bgrun.v1+xml",
		})
		if err != nil {
			return fmt.Errorf("ReleaseTransport %s poll: %w", transportNumber, err)
		}
		data, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		var bgRun struct {
			Status string `xml:"status"`
			Links  []struct {
				Rel  string `xml:"rel,attr"`
				Href string `xml:"href,attr"`
				Type string `xml:"type,attr"`
			} `xml:"link"`
		}
		if err := xml.Unmarshal(data, &bgRun); err != nil {
			return fmt.Errorf("ReleaseTransport %s: parsing poll response: %w", transportNumber, err)
		}

		switch strings.ToLower(bgRun.Status) {
		case "finished":
			// Try to fetch the result from the result link.
			for _, link := range bgRun.Links {
				if strings.Contains(link.Rel, "result") && link.Href != "" {
					return c.fetchReleaseResult(ctx, transportNumber, link.Href)
				}
			}
			return nil // finished without result link — assume OK
		case "failed", "processnotstarted":
			return fmt.Errorf("ReleaseTransport %s: background run status: %s", transportNumber, bgRun.Status)
		default:
			// Still running — continue polling.
		}
	}
}

// fetchReleaseResult fetches the result of a completed background release.
func (c *httpClient) fetchReleaseResult(ctx context.Context, transportNumber, resultURI string) error {
	resp, err := c.doRead(ctx, resultURI, map[string]string{
		"Accept": "application/vnd.sap.adt.transportorganizer.v1+xml",
	})
	if err != nil {
		return fmt.Errorf("ReleaseTransport %s result: %w", transportNumber, err)
	}
	data, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return checkReleaseResponse(transportNumber, data)
}

// GetTransportRequests lists CTS transport requests filtered by owner and status.
//
// user is the SAP username (e.g. "MMUSTERMANN") whose requests to return.
// Pass an empty string to omit the filter — but note that SAP CTS then scopes
// results to the authenticated technical user's context, not all system users.
// If you want a specific developer's requests (e.g. the person currently logged
// in via a BTP app), extract their username from the JWT and pass it here.
//
// status filters by request state: "D" = modifiable (open), "L" = released, "" = all.
//
// The returned slice includes requests from both workbench and customizing groups.
func (c *httpClient) GetTransportRequests(ctx context.Context, user, status string) ([]TransportRequest, error) {
	params := url.Values{}
	if user != "" {
		params.Set("user", user)
	}
	if status != "" {
		params.Set("status", status)
	}
	path := "/sap/bc/adt/cts/transportrequests"
	if len(params) > 0 {
		path += "?" + params.Encode()
	}

	resp, err := c.doRead(ctx, path, map[string]string{"Accept": "application/vnd.sap.adt.transportorganizertree.v1+xml"})
	if err != nil {
		return nil, fmt.Errorf("GetTransportRequests: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkResponse(resp); err != nil {
		return nil, err
	}

	data, _ := io.ReadAll(resp.Body)
	var root adtxml.TransportRoot
	if err := xml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("GetTransportRequests parsing: %w", err)
	}

	// Collect requests from all groups (workbench + customizing) and buckets (modifiable + released).
	var all []adtxml.TransportRequest
	for _, group := range []adtxml.TransportGroup{root.Workbench, root.Customizing} {
		all = append(all, group.Modifiable.Requests...)
		all = append(all, group.Released.Requests...)
	}

	result := make([]TransportRequest, len(all))
	for i, r := range all {
		result[i] = TransportRequest{
			Number: r.Number, Owner: r.Owner,
			Description: r.Description, Status: r.Status,
		}
	}

	// S/4HANA systems using KORRDEV=SYST/CUST have transports silently filtered
	// by the ADT endpoint, which only surfaces KORRDEV=K requests. Fall back to
	// querying E070/E07T directly when the ADT response is empty.
	if len(result) == 0 {
		return c.getTransportRequestsViaQuery(ctx, user, status)
	}
	return result, nil
}

// transportUserRe matches valid SAP usernames safe to embed in a SQL WHERE
// clause literal. SAP usernames are at most 40 chars: alphanumeric, _, -, ..
var transportUserRe = regexp.MustCompile(`^[A-Za-z0-9_.\-]{1,40}$`)

// transportStatusRe matches a valid CTS transport status: a single uppercase
// letter (D = modifiable, L = released, A = accepted, …).
var transportStatusRe = regexp.MustCompile(`^[A-Z]$`)

// getTransportRequestsViaQuery is the fallback for GetTransportRequests on
// S/4HANA systems where KORRDEV=SYST/CUST prevents the ADT transport organizer
// tree endpoint from returning any results. It queries E070 directly via the
// ADT data preview endpoint (JOINs are rejected on some S/4HANA releases).
func (c *httpClient) getTransportRequestsViaQuery(ctx context.Context, user, status string) ([]TransportRequest, error) {
	if user != "" && !transportUserRe.MatchString(user) {
		return nil, fmt.Errorf("GetTransportRequests: user %q contains characters not allowed in a transport query", user)
	}
	if status != "" && !transportStatusRe.MatchString(status) {
		return nil, fmt.Errorf("GetTransportRequests: status must be a single uppercase letter, got %q", status)
	}

	// Exclude tasks (STRKORR is set on tasks; top-level requests have STRKORR = '').
	where := []string{"STRKORR = ''"}
	if user != "" {
		where = append(where, "AS4USER = '"+user+"'")
	}
	if status != "" {
		where = append(where, "TRSTATUS = '"+status+"'")
	}

	// Single-table query: the ADT data preview endpoint rejects JOINs on some
	// S/4HANA releases. Description is not available here; callers that need it
	// can call GetTransportInfo per transport number.
	query := "SELECT TRKORR, AS4USER, TRSTATUS FROM E070 WHERE " +
		strings.Join(where, " AND ") + " ORDER BY TRKORR"

	qr, err := c.RunQuery(ctx, query, 5000)
	if err != nil {
		return nil, fmt.Errorf("GetTransportRequests: fallback E070 query: %w", err)
	}

	idx := queryColumnIndexes(qr, "TRKORR", "AS4USER", "TRSTATUS")
	if idx["TRKORR"] < 0 {
		return nil, fmt.Errorf("GetTransportRequests: fallback query returned no TRKORR column")
	}

	result := make([]TransportRequest, 0, len(qr.Rows))
	for _, row := range qr.Rows {
		tr := TransportRequest{
			Number: queryCell(row, idx["TRKORR"]),
			Owner:  queryCell(row, idx["AS4USER"]),
			Status: queryCell(row, idx["TRSTATUS"]),
		}
		if tr.Number != "" {
			result = append(result, tr)
		}
	}
	return result, nil
}

// queryColumnIndexes maps each requested column name to its position in
// qr.Columns, or to -1 when the result does not carry that column. Data
// preview results are addressed by column *name* rather than by position
// because the endpoint is free to reorder or omit columns; every query-route
// fallback in this file shares this lookup.
func queryColumnIndexes(qr *QueryResult, names ...string) map[string]int {
	idx := make(map[string]int, len(names))
	for _, name := range names {
		idx[name] = -1
	}
	for i, col := range qr.Columns {
		if _, wanted := idx[col.Name]; wanted {
			idx[col.Name] = i
		}
	}
	return idx
}

// queryCell returns the trimmed value of row[i], or "" when i is -1 (column
// absent from the result) or beyond the row's length (short row).
func queryCell(row []string, i int) string {
	if i < 0 || i >= len(row) {
		return ""
	}
	return strings.TrimSpace(row[i])
}

func (c *httpClient) AddToTransport(ctx context.Context, objectURI, transport string) error {
	body, err := xml.Marshal(adtxml.TransportComponent{
		NSCore:    nsADTCore,
		ObjectURI: objectURI,
	})
	if err != nil {
		return fmt.Errorf("marshal transport component: %w", err)
	}

	path := "/sap/bc/adt/cts/transportrequests/" + transport + "/abaptransportcomponents"
	resp, err := c.doMutate(ctx, http.MethodPost, path,
		strings.NewReader(xml.Header+string(body)),
		map[string]string{"Content-Type": contentTypeXML},
	)
	if err != nil {
		return fmt.Errorf("AddToTransport: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	return checkResponse(resp)
}

func (c *httpClient) RemoveFromTransport(ctx context.Context, taskNumber, parentTransport, pgmID, objectType, objectName, wbType, position string) error {
	body, err := xml.Marshal(adtxml.TMRoot{
		NSTM:       "http://www.sap.com/cts/adt/tm",
		UserAction: "removeobject",
		Number:     taskNumber,
		Request: adtxml.TMRequest{
			Number: parentTransport,
			Objects: []adtxml.TMAbapObject{{
				PgmID:    pgmID,
				Type:     objectType,
				Name:     objectName,
				WBType:   wbType,
				Position: position,
			}},
		},
	})
	if err != nil {
		return fmt.Errorf("marshal remove object request: %w", err)
	}

	path := "/sap/bc/adt/cts/transportrequests/" + taskNumber
	resp, err := c.doMutate(ctx, http.MethodPut, path,
		strings.NewReader(xml.Header+string(body)),
		map[string]string{
			"Content-Type": "application/vnd.sap.adt.transportorganizer.v1+xml",
			"Accept":       "application/vnd.sap.adt.transportorganizer.v1+xml",
		},
	)
	if err != nil {
		return fmt.Errorf("RemoveFromTransport: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	return checkResponse(resp)
}

// checkReleaseResponse parses the release job response and returns an error
// if the release failed. SAP returns HTTP 200 even on failure — the actual
// status is in chkrun:status attribute ("released" = OK, "abortrelapifail" = error).
func checkReleaseResponse(transportNumber string, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	var root struct {
		Reports struct {
			Report struct {
				Status     string `xml:"status,attr"`
				StatusText string `xml:"statusText,attr"`
				Messages   struct {
					Items []struct {
						Type      string `xml:"type,attr"`
						ShortText string `xml:"shortText,attr"`
					} `xml:"checkMessage"`
				} `xml:"checkMessageList"`
			} `xml:"checkReport"`
		} `xml:"releasereports"`
	}
	if err := xml.Unmarshal(data, &root); err != nil {
		return nil // can't parse — assume OK
	}
	if root.Reports.Report.Status == "" || root.Reports.Report.Status == "released" {
		return nil
	}
	// Collect error messages
	msg := root.Reports.Report.StatusText
	for _, m := range root.Reports.Report.Messages.Items {
		if m.Type == "E" {
			msg += ": " + m.ShortText
		}
	}
	return fmt.Errorf("ReleaseTransport %s failed: %s", transportNumber, msg)
}

// TransportObject describes an object recorded in a transport request.
type TransportObject struct {
	PgmID    string `json:"pgmid"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	WBType   string `json:"wb_type"`
	Position string `json:"position"`
	// Task is the number of the task that recorded this entry, empty when
	// the response did not attribute it to a task (e.g. a request-level
	// object with no task-level duplicate).
	Task string `json:"task,omitempty"`
}

// readTransportXML fetches the raw XML for a single transport request.
// Tries the given accept type first, falls back to the alternative if 406.
func (c *httpClient) readTransportXML(ctx context.Context, transportNumber, accept string) ([]byte, error) {
	path := "/sap/bc/adt/cts/transportrequests/" + url.PathEscape(transportNumber)
	resp, err := c.doRead(ctx, path, map[string]string{"Accept": accept})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkResponse(resp); err != nil {
		return nil, err
	}
	return io.ReadAll(resp.Body)
}

// GetTransportInfo retrieves status and description of a single transport by number.
func (c *httpClient) GetTransportInfo(ctx context.Context, transportNumber string) (*TransportRequest, error) {
	data, err := c.readTransportXML(ctx, transportNumber, "application/vnd.sap.adt.transportorganizer.v1+xml")
	if err != nil {
		return nil, fmt.Errorf("GetTransportInfo: %w", err)
	}
	return parseTransportInfo(data, transportNumber)
}

// GetTransportObjects reads the object list of a transport request, deduplicated across request and tasks.
//
// The ADT transport-organizer response is the primary source and is used
// whenever it carries the addressed request. Only when it does not — the
// absentTransportError case, which on ECC is every *released* request,
// because that endpoint's worklist holds only modifiable ones — does this
// fall back to reading E071 directly via getTransportObjectsViaQuery. The
// query is never issued when the ADT path succeeded: it is a second round
// trip and, on a system where the data preview endpoint is unavailable or
// unauthorised, a second way to fail.
//
// This leaves a deliberate asymmetry on ECC: a modifiable request resolves
// through ADT and carries no Position (the server sends none), while a
// released one resolves through E071 and does. Each is the best its source
// offers.
func (c *httpClient) GetTransportObjects(ctx context.Context, transportNumber string) ([]TransportObject, error) {
	data, err := c.readTransportXML(ctx, transportNumber, "application/vnd.sap.adt.transportorganizer.v1+xml, application/xml")
	if err != nil {
		return nil, fmt.Errorf("GetTransportObjects: %w", err)
	}
	objects, err := parseTransportObjectsXML(data, transportNumber)
	if err == nil {
		return objects, nil
	}
	if !errors.Is(err, errTransportAbsent) {
		return nil, err
	}

	objects, queryErr := c.getTransportObjectsViaQuery(ctx, transportNumber)
	switch {
	case queryErr == nil:
		return objects, nil
	case errors.Is(queryErr, errTransportNumberUnsafe):
		// The string could never have been a transport number. Leading with
		// "absent from the worklist" would bury the real reason behind a
		// finding that is true but beside the point.
		return nil, fmt.Errorf("GetTransportObjects: %w", queryErr)
	case errors.Is(queryErr, errTransportAbsent):
		// Both sources agree the request is not here. Say that once, in the
		// caller's own spelling of the number, rather than twice.
		return nil, fmt.Errorf("GetTransportObjects: %w; it has no E070 entry on this system either", err)
	default:
		// Name both attempts so the caller can tell "this request is not on
		// this system" from "the fallback could not run here".
		return nil, fmt.Errorf("GetTransportObjects: %w; the E071 query fallback did not resolve it either: %w", err, queryErr)
	}
}

// transportNumberRe matches a SAP transport request or task number that is
// safe to embed as a literal in a SQL WHERE clause. E070-TRKORR is CHAR20.
// "/" is admitted because namespaced requests are legitimate
// (/ACCGO/ACMS41709FP00), as are "-" and "." for SAP's own piece lists
// (SAPK-70003INSAPBW). Everything else — quotes of either kind, whitespace,
// backslashes, semicolons, parentheses, %, comment markers — is rejected, so
// a validated value cannot terminate or escape the literal it goes into.
var transportNumberRe = regexp.MustCompile(`^[A-Za-z0-9_/.\-]{1,20}$`)

// errTransportNumberUnsafe is the sentinel behind the validation failure
// transportNumberRe produces, so GetTransportObjects can report an unusable
// number as the reason rather than burying it behind the worklist finding.
var errTransportNumberUnsafe = errors.New("contains characters not allowed in a transport query")

// pgmIDReleaseMarker is the E071 PGMID of a release marker row (paired with
// OBJECT "RELE"). Such a row is bookkeeping, not a repository object: its
// OBJ_NAME is a packed audit string like "E20K928234 20160702 143007 U13409".
// Released requests always carry one, so the E071 fallback must not hand it
// to callers as if it were transported content.
const pgmIDReleaseMarker = "CORR"

// getTransportObjectsViaQuery reads a transport request's objects straight
// out of E071 via the ADT data preview endpoint. It is the fallback for
// GetTransportObjects when the transport-organizer response does not contain
// the addressed request — on ECC that is every released request, and
// RollbackTransport exists precisely for released, imported transports.
//
// Two queries, because a modifiable request's object entries live on its task
// rows as well as on its own: E070 yields the task numbers, then one E071
// query covers the request and all of its tasks. A released request — the
// main case here — usually has no tasks left at all: releasing dissolves them
// and compresses their entries onto the request itself, so the E070 query
// legitimately returns zero rows and the E071 query addresses the request
// alone. Both are single-table (the data preview endpoint rejects JOINs on
// some S/4HANA releases) and address their result columns by name. The second
// query combines the numbers with OR rather than IN, which is not verified as
// accepted on both system families. Neither query uses ORDER BY ... DESC: the
// data preview endpoint rejects a descending sort ("Die Elemente der ORDER
// BY-Liste müssen mit Kommata getrennt werden").
//
// Each row maps to a TransportObject with the row's own TRKORR as Task —
// except for rows sitting on the request itself, which stay Task-less, the
// same way a request-level object does on the ADT path. On a released request
// that is nearly every row. WBType has no E071 column and is always empty
// here.
//
// Two properties of E071 that callers must know, neither of which is
// normalised away here:
//
//   - E071 records more than repository objects. A released request carries a
//     release marker row (PGMID CORR, OBJECT RELE) whose OBJ_NAME is a packed
//     audit string such as "E20K928234 20160702 143007 U13409", not an object
//     name. PGMID CORR is excluded in the query itself, so the exclusion is
//     visible in the statement rather than buried in a post-filter; the row
//     loop drops any that survive anyway, as a guard against a server that
//     ignores the predicate.
//   - E071 records sub-object granularity. A single method arrives as its own
//     LIMU METH row, where the ADT XML path reports the owning class once at
//     R3TR granularity. The two paths therefore do not return identical
//     shapes for the same transport, and this one is not folded up to match.
//
// transportNumber is validated against transportNumberRe before it is
// interpolated, and so is every task number the first query returns. It is
// then uppercased: SAP stores TRKORR uppercase and Open SQL "=" on CHAR is
// case-sensitive, so a caller's lowercase number would match no row and this
// path would answer "no objects" where the ADT path — deliberately
// case-insensitive, see matchesTransportNumber — answers correctly.
func (c *httpClient) getTransportObjectsViaQuery(ctx context.Context, transportNumber string) ([]TransportObject, error) {
	if !transportNumberRe.MatchString(transportNumber) {
		return nil, fmt.Errorf("transport number %q %w", transportNumber, errTransportNumberUnsafe)
	}
	// Uppercase once, after validation, and use this value for both
	// statements and for the task-attribution comparison below.
	number := strings.ToUpper(transportNumber)

	numbers, err := c.transportQueryNumbers(ctx, number)
	if err != nil {
		return nil, err
	}

	where := make([]string, 0, len(numbers))
	for _, n := range numbers {
		where = append(where, "TRKORR = '"+n+"'")
	}
	// The TRKORR disjunction is parenthesised so the PGMID exclusion applies
	// to all of it and not just the last alternative. Spaces around the
	// parentheses keep the statement acceptable to Open SQL's stricter
	// tokenizer.
	query := "SELECT TRKORR, AS4POS, PGMID, OBJECT, OBJ_NAME FROM E071 WHERE ( " +
		strings.Join(where, " OR ") + " ) AND PGMID <> '" + pgmIDReleaseMarker + "'" +
		" ORDER BY TRKORR, AS4POS"

	qr, err := c.RunQuery(ctx, query, 5000)
	if err != nil {
		return nil, fmt.Errorf("E071 object query: %w", err)
	}
	idx := queryColumnIndexes(qr, "TRKORR", "AS4POS", "PGMID", "OBJECT", "OBJ_NAME")
	if idx["OBJ_NAME"] < 0 {
		return nil, fmt.Errorf("E071 object query returned no OBJ_NAME column")
	}

	// Same deduper, identity and upgrade rule as the ADT path, so both agree.
	dedup := newTransportObjectDeduper()
	for _, row := range qr.Rows {
		// The query already excludes release markers; this repeats the rule
		// only so a server that ignores the predicate cannot put a packed
		// audit string into a caller's object list.
		if strings.EqualFold(queryCell(row, idx["PGMID"]), pgmIDReleaseMarker) {
			continue
		}
		task := queryCell(row, idx["TRKORR"])
		if strings.EqualFold(task, number) {
			task = "" // a row on the request itself is not task-attributed
		}
		dedup.add(
			queryCell(row, idx["PGMID"]),
			queryCell(row, idx["OBJECT"]),
			queryCell(row, idx["OBJ_NAME"]),
			"", // WBType: no E071 column
			queryCell(row, idx["AS4POS"]),
			task,
		)
	}
	return dedup.result(), nil
}

// transportQueryNumbers returns transportNumber followed by the numbers of
// its tasks, read from E070. One query answers two questions, so the fallback
// needs no extra round trip to tell them apart:
//
//   - Does the request exist here at all? A row whose TRKORR is the number is
//     the request's own header row and proves it does. If the query returns
//     no row whatsoever, neither the request nor any task of it is on this
//     system, and the answer is absentTransportError — not an empty object
//     list. Task 2's contract (see absentTransportError) is that a silently
//     empty result must never be the answer to "the server does not have that
//     request", and the E071 path would otherwise drop that contract: the
//     CORR exclusion means a real released request and a nonexistent one both
//     come back with zero object rows, so the row count alone cannot tell
//     them apart.
//   - Which tasks does it have? Rows whose STRKORR is the number. A released
//     request usually has none — releasing dissolves its tasks.
//
// transportNumber must already have passed transportNumberRe and been
// uppercased; getTransportObjectsViaQuery is the only caller and does both.
// Task numbers coming back from the server are re-validated here before they
// reach the E071 statement, exactly like the caller's own number; duplicates
// and the request's own row are dropped.
func (c *httpClient) transportQueryNumbers(ctx context.Context, transportNumber string) ([]string, error) {
	query := "SELECT TRKORR, STRKORR FROM E070 WHERE TRKORR = '" + transportNumber +
		"' OR STRKORR = '" + transportNumber + "' ORDER BY TRKORR"
	qr, err := c.RunQuery(ctx, query, 5000)
	if err != nil {
		return nil, fmt.Errorf("E070 request/task query: %w", err)
	}
	idx := queryColumnIndexes(qr, "TRKORR", "STRKORR")
	if idx["TRKORR"] < 0 {
		return nil, fmt.Errorf("E070 request/task query returned no TRKORR column")
	}
	if len(qr.Rows) == 0 {
		return nil, absentTransportError(transportNumber)
	}

	numbers := []string{transportNumber}
	seen := map[string]bool{transportNumber: true}
	for _, row := range qr.Rows {
		// Only rows parented by this request are its tasks; the request's own
		// header row comes back from the same query and is already in numbers.
		if !strings.EqualFold(queryCell(row, idx["STRKORR"]), transportNumber) {
			continue
		}
		task := strings.ToUpper(queryCell(row, idx["TRKORR"]))
		if task == "" || seen[task] || !transportNumberRe.MatchString(task) {
			continue
		}
		seen[task] = true
		numbers = append(numbers, task)
	}
	return numbers, nil
}

// GetTransportTasks returns the task numbers belonging to a transport request.
func (c *httpClient) GetTransportTasks(ctx context.Context, transportNumber string) ([]string, error) {
	data, err := c.readTransportXML(ctx, transportNumber, "application/vnd.sap.adt.transportorganizer.v1+xml, application/xml")
	if err != nil {
		return nil, fmt.Errorf("GetTransportTasks: %w", err)
	}
	return parseTransportTaskNumbers(data, transportNumber)
}

// parseTransportInfo extracts a single request's Number/Owner/Description/
// Status from either shape a transport-request GET may come back in — see
// xmlTransportDoc. It shares that struct and matchesTransportNumber/
// walkRequests with parseTransportObjectsXML and parseTransportTaskNumbers so
// all three parsers on this response body agree on what "this is the
// addressed request" and "this request is absent" mean.
func parseTransportInfo(data []byte, transportNumber string) (*TransportRequest, error) {
	var doc xmlTransportDoc
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing transport info: %w", err)
	}

	toTransportRequest := func(req xmlRequest) *TransportRequest {
		return &TransportRequest{
			Number:      req.Number,
			Owner:       req.Owner,
			Description: req.Description,
			Status:      req.Status,
		}
	}

	// Format 1: a single request directly under <tm:root>
	// (transportorganizer.v1). A body whose number differs from
	// transportNumber is treated as absent, identically to the Format 2
	// (worklist) branch below. An empty transportNumber, or a match, keeps
	// the existing behaviour.
	if doc.Request.Number != "" {
		if transportNumber == "" || strings.EqualFold(doc.Request.Number, transportNumber) {
			return toTransportRequest(doc.Request), nil
		}
		return nil, absentTransportError(transportNumber)
	}

	// Format 2: workbench/customizing > sections > requests (application/xml)
	// — this is also ECC's worklist shape (see eccWorklistXML). Select the
	// request whose number matches transportNumber.
	var result *TransportRequest
	found := doc.walkRequests(transportNumber, func(req xmlRequest) {
		if result == nil {
			result = toTransportRequest(req)
		}
	})
	if !found {
		return nil, absentTransportError(transportNumber)
	}
	return result, nil
}

// absentTransportError reports that transportNumber was not found anywhere in
// a parsed transport-organizer response body. This is distinct from a request
// that is present but holds no objects/tasks (which returns an empty slice
// and a nil error) — a silently empty list must not be the answer to "the
// server did not send me that request". On ECC, the transport-organizer
// worklist endpoint (Format 2 below) returns only modifiable requests, so a
// released request is always "absent" by this definition; Task 4 adds an
// E071-based fallback for that case.
func absentTransportError(transportNumber string) error {
	return fmt.Errorf("transport %s is %w; "+
		"on ECC that endpoint returns only modifiable requests, so released requests cannot be read this way",
		transportNumber, errTransportAbsent)
}

// errTransportAbsent is the sentinel every absentTransportError wraps, so a
// caller can branch on "the server did not send me that request" with
// errors.Is instead of matching the message text. GetTransportObjects uses it
// to decide whether its E071 query fallback applies.
var errTransportAbsent = errors.New("not in this system's transport-organizer worklist")

// matchesTransportNumber reports whether reqNumber (a request's tm:number
// attribute, possibly empty) identifies transportNumber. The comparison is
// case-insensitive because aibap.mcp passes the caller's string through
// unchanged. A request with an empty or absent number never matches — this
// is the fix for the historical guard here, which let unnumbered requests
// leak into every result.
func matchesTransportNumber(reqNumber, transportNumber string) bool {
	if reqNumber == "" || transportNumber == "" {
		return false
	}
	return strings.EqualFold(reqNumber, transportNumber)
}

// xmlObject is a single tm:abap_object entry recorded against a request or task.
type xmlObject struct {
	PgmID    string `xml:"pgmid,attr"`
	Type     string `xml:"type,attr"`
	Name     string `xml:"name,attr"`
	WBType   string `xml:"wbtype,attr"`
	Position string `xml:"position,attr"`
}

// xmlObjectGroup binds the <tm:all_objects> wrapper some responses (real S/4
// single-request bodies) nest object lists inside. It is bound at both
// request and task level; xmlRequest.objects and xmlTask.objects merge it
// with any bare, direct-child <tm:abap_object> elements (ECC's shape) so
// neither is lost.
type xmlObjectGroup struct {
	Objects []xmlObject `xml:"abap_object"`
}

// xmlTask is a <tm:task> element nested under a request.
type xmlTask struct {
	Number     string         `xml:"number,attr"`
	Objects    []xmlObject    `xml:"abap_object"`
	AllObjects xmlObjectGroup `xml:"all_objects"`
}

// objects returns this task's objects, merging bare direct children with any
// wrapped under <tm:all_objects>.
func (t xmlTask) objects() []xmlObject {
	if len(t.AllObjects.Objects) == 0 {
		return t.Objects
	}
	return append(append([]xmlObject{}, t.Objects...), t.AllObjects.Objects...)
}

// xmlRequest is a <tm:request> element, either the sole element of a Format 1
// (transportorganizer.v1) body or one of many under a Format 2 (application/xml)
// workbench/customizing group. Owner, Description and Status are carried here
// (not just Number) so a caller needing them — see GetTransportInfo — can use
// this same struct instead of a parallel one.
type xmlRequest struct {
	Number      string         `xml:"number,attr"`
	Owner       string         `xml:"owner,attr"`
	Description string         `xml:"desc,attr"`
	Status      string         `xml:"status,attr"`
	Objects     []xmlObject    `xml:"abap_object"`
	AllObjects  xmlObjectGroup `xml:"all_objects"`
	Tasks       []xmlTask      `xml:"task"`
}

// objects returns this request's own (non-task) objects, merging bare direct
// children with any wrapped under <tm:all_objects>. See the a0 fix note on
// xmlObjectGroup: request-level objects wrapped in <tm:all_objects> (the real
// S/4 shape once a request has been sorted-and-compressed, or is released)
// were silently dropped before this method existed.
func (r xmlRequest) objects() []xmlObject {
	if len(r.AllObjects.Objects) == 0 {
		return r.Objects
	}
	return append(append([]xmlObject{}, r.Objects...), r.AllObjects.Objects...)
}

// xmlTransportGroup is a Format 2 group (<tm:workbench> or <tm:customizing>),
// each holding one or more section elements — named "tm:modifiable",
// "tm:released", or generically "section" depending on server and vintage —
// that in turn hold <tm:request> elements. The section name itself carries no
// meaning to any parser here, so it is matched with xml:",any".
type xmlTransportGroup struct {
	Sections []struct {
		Requests []xmlRequest `xml:"request"`
	} `xml:",any"`
}

// xmlTransportDoc is the parsed shape of a transport-request response body,
// covering both formats a single request may arrive in:
//
//   - Format 1 (application/vnd.sap.adt.transportorganizer.v1+xml): a single
//     <tm:request> directly under <tm:root>.
//   - Format 2 (application/xml): one or more <tm:request> elements grouped
//     under <tm:workbench> and/or <tm:customizing>. This is also the shape of
//     ECC's worklist response — see eccWorklistXML in transport_ecc_test.go.
//
// Both parseTransportObjectsXML and parseTransportTaskNumbers walk this same
// document; GetTransportRequests (which already walks both the workbench and
// customizing groups) is the precedent for binding both here instead of only
// workbench.
type xmlTransportDoc struct {
	Request     xmlRequest        `xml:"request"`
	Workbench   xmlTransportGroup `xml:"workbench"`
	Customizing xmlTransportGroup `xml:"customizing"`
}

// walkRequests calls fn for every Format 2 request (across the workbench and
// customizing groups) whose number matches transportNumber per
// matchesTransportNumber, and reports whether at least one matched — the
// "present" half of the absent/empty distinction.
func (doc xmlTransportDoc) walkRequests(transportNumber string, fn func(xmlRequest)) (found bool) {
	for _, group := range []xmlTransportGroup{doc.Workbench, doc.Customizing} {
		for _, section := range group.Sections {
			for _, req := range section.Requests {
				if !matchesTransportNumber(req.Number, transportNumber) {
					continue
				}
				found = true
				fn(req)
			}
		}
	}
	return found
}

func parseTransportTaskNumbers(data []byte, transportNumber string) ([]string, error) {
	var doc xmlTransportDoc
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing transport tasks: %w", err)
	}

	var tasks []string
	addTasks := func(req xmlRequest) {
		for _, task := range req.Tasks {
			if task.Number != "" {
				tasks = append(tasks, task.Number)
			}
		}
	}

	// Format 1: a Format 1 body whose number differs from transportNumber is
	// treated as absent, identically to the Format 2 (worklist) branch below.
	// An empty transportNumber, or a match, keeps the existing behaviour.
	if doc.Request.Number != "" {
		if transportNumber == "" || strings.EqualFold(doc.Request.Number, transportNumber) {
			addTasks(doc.Request)
			return tasks, nil
		}
		return nil, absentTransportError(transportNumber)
	}

	// Format 2
	if found := doc.walkRequests(transportNumber, addTasks); !found {
		return nil, absentTransportError(transportNumber)
	}
	return tasks, nil
}

// transportObjectDeduper collects TransportObjects from one or more sources
// — the XML object list here, and Task 4's database-query fallback — into a
// single deduplicated, order-stable list. Objects are keyed by
// pgmid/type/name; the first occurrence of a key establishes every field
// (including Position). A later occurrence for the same key never replaces
// the entry, except that if the existing entry has no Task and the new
// occurrence carries one, the entry is upgraded in place — this is how a
// request-level object also recorded under a task ends up attributed to
// that task without losing its first-seen Position or ordering.
type transportObjectDeduper struct {
	index   map[string]int
	objects []TransportObject
}

// newTransportObjectDeduper returns an empty deduper ready for add.
func newTransportObjectDeduper() *transportObjectDeduper {
	return &transportObjectDeduper{index: make(map[string]int)}
}

// add records one object occurrence, attributed to task (pass "" for a
// request-level occurrence not nested in a task). Empty names are ignored.
func (d *transportObjectDeduper) add(pgmid, typ, name, wbtype, position, task string) {
	if name == "" {
		return
	}
	key := pgmid + "/" + typ + "/" + name
	if idx, ok := d.index[key]; ok {
		if d.objects[idx].Task == "" && task != "" {
			d.objects[idx].Task = task
		}
		return
	}
	d.index[key] = len(d.objects)
	d.objects = append(d.objects, TransportObject{
		PgmID: pgmid, Type: typ, Name: name, WBType: wbtype, Position: position, Task: task,
	})
}

// result returns the deduplicated objects in first-seen order.
func (d *transportObjectDeduper) result() []TransportObject {
	return d.objects
}

func parseTransportObjectsXML(data []byte, transportNumber string) ([]TransportObject, error) {
	var doc xmlTransportDoc
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing transport objects: %w", err)
	}

	dedup := newTransportObjectDeduper()
	addFromRequest := func(req xmlRequest) {
		for _, obj := range req.objects() {
			dedup.add(obj.PgmID, obj.Type, obj.Name, obj.WBType, obj.Position, "")
		}
		for _, task := range req.Tasks {
			for _, obj := range task.objects() {
				dedup.add(obj.PgmID, obj.Type, obj.Name, obj.WBType, obj.Position, task.Number)
			}
		}
	}

	// Format 1: direct request under root (transportorganizer.v1). A body
	// whose number differs from transportNumber is treated as absent,
	// identically to the Format 2 (worklist) branch below. An empty
	// transportNumber, or a match, keeps the existing behaviour.
	if doc.Request.Number != "" {
		if transportNumber == "" || strings.EqualFold(doc.Request.Number, transportNumber) {
			addFromRequest(doc.Request)
			return dedup.result(), nil
		}
		return nil, absentTransportError(transportNumber)
	}

	// Format 2: workbench/customizing > sections > requests (application/xml),
	// filtered to the addressed transport. A request with an empty or absent
	// number never matches (matchesTransportNumber), so it can no longer leak
	// its objects into every result the way the old guard allowed.
	if found := doc.walkRequests(transportNumber, addFromRequest); !found {
		return nil, absentTransportError(transportNumber)
	}
	return dedup.result(), nil
}
