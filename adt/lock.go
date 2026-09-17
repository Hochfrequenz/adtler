package adt

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/Hochfrequenz/adtler/adt/adtxml"
)

func (c *httpClient) LockObject(ctx context.Context, objectURI string) (string, error) {
	resp, err := c.doMutate(ctx, http.MethodPost,
		objectURI+"?_action=LOCK&accessMode=MODIFY",
		nil,
		map[string]string{
			"Accept":                "application/vnd.sap.as+xml;charset=UTF-8;dataname=com.sap.adt.lock.result",
			"X-sap-adt-sessiontype": "stateful",
		},
	)
	if err != nil {
		return "", fmt.Errorf("LockObject: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkResponse(resp); err != nil {
		return "", err
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("LockObject reading body: %w", err)
	}
	// SAP returns asx:abap envelope with LockData
	lockData, unmarshalErr := adtxml.UnmarshalASXData[adtxml.LockData](data)
	if unmarshalErr == nil && lockData.LockHandle != "" {
		return lockData.LockHandle, nil
	}
	// Fallback: if response is not XML, treat entire body as handle
	handle := strings.TrimSpace(string(data))
	if handle == "" {
		return "", fmt.Errorf("LockObject: empty lock handle in response")
	}
	return handle, nil
}

func (c *httpClient) UnlockObject(ctx context.Context, objectURI, lockHandle string) error {
	// Real lock handles commonly contain '+', '=', '/' (base64-shaped).
	// Unescaped, a '+' is decoded as a space by any form-urlencoded-convention
	// query parser, silently corrupting the handle the server receives —
	// see aibap.mcp#494's raw-HTTP diagnosis, which required URL-encoding to
	// get a real release. url.QueryEscape matches how the other
	// lockHandle-in-query call sites build their query string via url.Values.
	resp, err := c.doMutate(ctx, http.MethodPost,
		objectURI+"?_action=UNLOCK&lockHandle="+url.QueryEscape(lockHandle),
		nil,
		map[string]string{"X-sap-adt-sessiontype": "stateful"},
	)
	if err != nil {
		return fmt.Errorf("UnlockObject: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	return checkResponse(resp)
}
