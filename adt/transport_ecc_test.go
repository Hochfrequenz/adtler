package adt_test

import (
	"encoding/xml"
	"strings"
	"testing"
)

// The fixtures below were captured on 2026-09-16 by a temporary, read-only
// probe (adt/probe_transport_capture_integration_test.go, deleted before
// commit — see Task 1 of docs/superpowers/plans/2026-09-16-ecc-transport-parsing-plan.md)
// that issued GET /sap/bc/adt/cts/transportrequests/<number> against two real
// systems:
//
//   - the ECC system (SAP_BASIS 750), Accept: "application/vnd.sap.adt.transportorganizer.v1+xml, application/xml"
//     — the same Accept header GetTransportObjects/GetTransportTasks already send.
//   - the S/4 system (SAP_BASIS 816), same Accept header.
//
// Transport numbers, owners, object/package names and descriptions below have
// been replaced with synthetic values (a "DEV"/"S4D" system prefix in place of
// the real one, generic owner and object names) before this file was
// committed; the element names, attribute names, and atom relation URIs are
// verbatim from the capture.
//
// eccWorklistXML and eccWorklistEmptyNumberXML/eccCustomizingXML are hand
// -reduced from a genuine capture: requesting the single, unrelated
// transport DEVK902952 returned ECC's *entire* modifiable worklist for the
// authenticated user (129 distinct transport numbers, 2496 objects) instead
// of just that one transport — this is the ECC bug this plan's later tasks
// fix. Only two of those 129 requests (DEVK900178 and DEVK902952 itself,
// each reduced to a single task with a single object) are kept here; every
// element name, attribute name, and atom relation URI below is verbatim
// from that capture.
//
// s4SingleRequestXML and s4RequestNoObjectsXML are hand-reduced from genuine
// S/4 captures of real transports (S4DK904438 and S4DK904476 respectively);
// only the boilerplate administrative atom:link entries (consistencycheck,
// sortandcompress, changeowner, etc.) were trimmed for size. s4ObjectAtBothLevelsXML
// is hand-built from s4SingleRequestXML's content: no captured S/4 response
// was found where an object sits both as a bare child of <tm:request> and
// inside a <tm:task>, so this fixture constructs that shape by hand instead.

// eccWorklistXML is ECC's worklist shape as captured: a GET for a single
// transport number instead returns the whole <tm:workbench><tm:modifiable>
// worklist for the authenticated user. Note the section element beneath the
// group is really named "tm:modifiable" (not "section" — that name comes
// only from the existing Go parsers' xml:",any" match, not from the wire).
// There is no position attribute anywhere in this shape.
const eccWorklistXML = `<?xml version="1.0" encoding="utf-8"?>` +
	`<tm:root adtcore:name="TESTUSER1" adtcore:changedAt="2026-09-16T13:24:09Z" adtcore:createdAt="2026-09-16T13:24:09Z" adtcore:changedBy="TESTUSER1" adtcore:createdBy="TESTUSER1" xmlns:tm="http://www.sap.com/cts/adt/tm" xmlns:adtcore="http://www.sap.com/adt/core">` +
	`<tm:workbench tm:category="Workbench">` +
	`<tm:modifiable tm:status="Änderbar">` +
	`<tm:request tm:number="DEVK900178" tm:owner="TESTUSER2" tm:desc="Feature transport" tm:status="D" tm:uri="/sap/bc/adt/vit/wb/object_type/%20%20%20%20rq/object_name/DEVK900178">` +
	`<atom:link href="/sap/bc/adt/cts/transportrequests/DEVK900178/consistencychecks" rel="http://www.sap.com/cts/relations/consistencycheck" type="application/xml" title="Transport Organizer Request/Task Consistency Check" xmlns:atom="http://www.w3.org/2005/Atom"/>` +
	`<atom:link href="/sap/bc/adt/cts/transportrequests/DEVK900178/releasejobs" rel="http://www.sap.com/cts/relations/releasejobs" type="application/xml" title="Transport Organizer Request/Task Release" xmlns:atom="http://www.w3.org/2005/Atom"/>` +
	`<atom:link href="/sap/bc/adt/cts/transportrequests/DEVK900178" rel="http://www.sap.com/cts/relations/modify" type="application/xml" title="Transport Organizer Request/Modify" xmlns:atom="http://www.w3.org/2005/Atom"/>` +
	`<atom:link href="/sap/bc/adt/cts/transportrequests/DEVK900178/tasks" rel="http://www.sap.com/cts/relations/newtask" type="application/xml" title="Transport Organizer New Task Creation" xmlns:atom="http://www.w3.org/2005/Atom"/>` +
	`<tm:task tm:number="DEVK900635" tm:owner="TESTUSER1" tm:desc="Entwicklung/Korrektur" tm:status="R" tm:uri="/sap/bc/adt/vit/wb/object_type/%20%20%20%20rq/object_name/DEVK900635">` +
	`<atom:link href="/sap/bc/adt/cts/transportrequests/DEVK900635/consistencychecks" rel="http://www.sap.com/cts/relations/consistencycheck" type="application/xml" title="Transport Organizer Request/Task Consistency Check" xmlns:atom="http://www.w3.org/2005/Atom"/>` +
	`<atom:link href="/sap/bc/adt/cts/transportrequests/DEVK900635/releasejobs" rel="http://www.sap.com/cts/relations/releasejobs" type="application/xml" title="Transport Organizer Request/Task Release" xmlns:atom="http://www.w3.org/2005/Atom"/>` +
	`<atom:link href="/sap/bc/adt/cts/transportrequests/DEVK900635" rel="http://www.sap.com/cts/relations/modify" type="application/xml" title="Transport Organizer Request/Modify" xmlns:atom="http://www.w3.org/2005/Atom"/>` +
	`<tm:abap_object tm:pgmid="R3TR" tm:type="PROG" tm:name="/ZTEST/ORDER_REQUEST" tm:wbtype="PROG/P" tm:dummy_uri="/sap/bc/adt/cts/transportrequests/reference?obj_name=/ZTEST/ORDER_REQUEST&amp;obj_wbtype=PROG&amp;pgmid=R3TR" tm:obj_info="Programm"/>` +
	`</tm:task>` +
	`</tm:request>` +
	`<tm:request tm:number="DEVK902952" tm:owner="TESTUSER1" tm:desc="Lock reproducer probe (throwaway, delete after)" tm:status="D" tm:uri="/sap/bc/adt/vit/wb/object_type/%20%20%20%20rq/object_name/DEVK902952">` +
	`<atom:link href="/sap/bc/adt/cts/transportrequests/DEVK902952/consistencychecks" rel="http://www.sap.com/cts/relations/consistencycheck" type="application/xml" title="Transport Organizer Request/Task Consistency Check" xmlns:atom="http://www.w3.org/2005/Atom"/>` +
	`<atom:link href="/sap/bc/adt/cts/transportrequests/DEVK902952/releasejobs" rel="http://www.sap.com/cts/relations/releasejobs" type="application/xml" title="Transport Organizer Request/Task Release" xmlns:atom="http://www.w3.org/2005/Atom"/>` +
	`<atom:link href="/sap/bc/adt/cts/transportrequests/DEVK902952" rel="http://www.sap.com/cts/relations/modify" type="application/xml" title="Transport Organizer Request/Modify" xmlns:atom="http://www.w3.org/2005/Atom"/>` +
	`<atom:link href="/sap/bc/adt/cts/transportrequests/DEVK902952/tasks" rel="http://www.sap.com/cts/relations/newtask" type="application/xml" title="Transport Organizer New Task Creation" xmlns:atom="http://www.w3.org/2005/Atom"/>` +
	`<tm:task tm:number="DEVK902953" tm:owner="TESTUSER1" tm:desc="Entwicklung/Korrektur" tm:status="D" tm:uri="/sap/bc/adt/vit/wb/object_type/%20%20%20%20rq/object_name/DEVK902953">` +
	`<atom:link href="/sap/bc/adt/cts/transportrequests/DEVK902953/consistencychecks" rel="http://www.sap.com/cts/relations/consistencycheck" type="application/xml" title="Transport Organizer Request/Task Consistency Check" xmlns:atom="http://www.w3.org/2005/Atom"/>` +
	`<atom:link href="/sap/bc/adt/cts/transportrequests/DEVK902953/releasejobs" rel="http://www.sap.com/cts/relations/releasejobs" type="application/xml" title="Transport Organizer Request/Task Release" xmlns:atom="http://www.w3.org/2005/Atom"/>` +
	`<atom:link href="/sap/bc/adt/cts/transportrequests/DEVK902953" rel="http://www.sap.com/cts/relations/modify" type="application/xml" title="Transport Organizer Request/Modify" xmlns:atom="http://www.w3.org/2005/Atom"/>` +
	`<tm:abap_object tm:pgmid="R3TR" tm:type="CLAS" tm:name="ZCL_LOCKREPRO_2" tm:wbtype="CLAS/OC" tm:dummy_uri="/sap/bc/adt/cts/transportrequests/reference?obj_name=ZCL_LOCKREPRO_2&amp;obj_wbtype=CLAS&amp;pgmid=R3TR" tm:obj_info="Klasse (ABAP Objects)"/>` +
	`</tm:task>` +
	`</tm:request>` +
	`</tm:modifiable>` +
	`</tm:workbench>` +
	`</tm:root>`

// eccWorklistEmptyNumberXML is eccWorklistXML hand-edited so the second
// request (DEVK902952) carries number="" while still holding its task and
// object. Task 2's filter rule (which requests to keep from a worklist) is
// defined against this.
var eccWorklistEmptyNumberXML = strings.Replace(eccWorklistXML,
	`tm:request tm:number="DEVK902952"`, `tm:request tm:number=""`, 1)

// eccCustomizingXML is eccWorklistXML hand-edited so the second request
// (DEVK902952, with its task and object left untouched) sits under a
// <tm:customizing> group instead of <tm:workbench>. No genuine ECC
// customizing-group capture was available — the probed worklist held only
// workbench entries — so the group wrapper here is inferred by symmetry: it
// reuses the real tm:category/tm:status attribute names and values already
// confirmed on the tm:workbench/tm:modifiable pair above. GetTransportObjects
// must not drop this request just because it sits in the customizing group.
const eccCustomizingXML = `<?xml version="1.0" encoding="utf-8"?>` +
	`<tm:root adtcore:name="TESTUSER1" adtcore:changedAt="2026-09-16T13:24:09Z" adtcore:createdAt="2026-09-16T13:24:09Z" adtcore:changedBy="TESTUSER1" adtcore:createdBy="TESTUSER1" xmlns:tm="http://www.sap.com/cts/adt/tm" xmlns:adtcore="http://www.sap.com/adt/core">` +
	`<tm:workbench tm:category="Workbench">` +
	`<tm:modifiable tm:status="Änderbar">` +
	`<tm:request tm:number="DEVK900178" tm:owner="TESTUSER2" tm:desc="Feature transport" tm:status="D" tm:uri="/sap/bc/adt/vit/wb/object_type/%20%20%20%20rq/object_name/DEVK900178">` +
	`<atom:link href="/sap/bc/adt/cts/transportrequests/DEVK900178" rel="http://www.sap.com/cts/relations/modify" type="application/xml" title="Transport Organizer Request/Modify" xmlns:atom="http://www.w3.org/2005/Atom"/>` +
	`<tm:task tm:number="DEVK900635" tm:owner="TESTUSER1" tm:desc="Entwicklung/Korrektur" tm:status="R" tm:uri="/sap/bc/adt/vit/wb/object_type/%20%20%20%20rq/object_name/DEVK900635">` +
	`<atom:link href="/sap/bc/adt/cts/transportrequests/DEVK900635" rel="http://www.sap.com/cts/relations/modify" type="application/xml" title="Transport Organizer Request/Modify" xmlns:atom="http://www.w3.org/2005/Atom"/>` +
	`<tm:abap_object tm:pgmid="R3TR" tm:type="PROG" tm:name="/ZTEST/ORDER_REQUEST" tm:wbtype="PROG/P" tm:dummy_uri="/sap/bc/adt/cts/transportrequests/reference?obj_name=/ZTEST/ORDER_REQUEST&amp;obj_wbtype=PROG&amp;pgmid=R3TR" tm:obj_info="Programm"/>` +
	`</tm:task>` +
	`</tm:request>` +
	`</tm:modifiable>` +
	`</tm:workbench>` +
	`<tm:customizing tm:category="Customizing">` +
	`<tm:modifiable tm:status="Änderbar">` +
	`<tm:request tm:number="DEVK902952" tm:owner="TESTUSER1" tm:desc="Lock reproducer probe (throwaway, delete after)" tm:status="D" tm:uri="/sap/bc/adt/vit/wb/object_type/%20%20%20%20rq/object_name/DEVK902952">` +
	`<atom:link href="/sap/bc/adt/cts/transportrequests/DEVK902952" rel="http://www.sap.com/cts/relations/modify" type="application/xml" title="Transport Organizer Request/Modify" xmlns:atom="http://www.w3.org/2005/Atom"/>` +
	`<tm:task tm:number="DEVK902953" tm:owner="TESTUSER1" tm:desc="Entwicklung/Korrektur" tm:status="D" tm:uri="/sap/bc/adt/vit/wb/object_type/%20%20%20%20rq/object_name/DEVK902953">` +
	`<atom:link href="/sap/bc/adt/cts/transportrequests/DEVK902953" rel="http://www.sap.com/cts/relations/modify" type="application/xml" title="Transport Organizer Request/Modify" xmlns:atom="http://www.w3.org/2005/Atom"/>` +
	`<tm:abap_object tm:pgmid="R3TR" tm:type="CLAS" tm:name="ZCL_LOCKREPRO_2" tm:wbtype="CLAS/OC" tm:dummy_uri="/sap/bc/adt/cts/transportrequests/reference?obj_name=ZCL_LOCKREPRO_2&amp;obj_wbtype=CLAS&amp;pgmid=R3TR" tm:obj_info="Klasse (ABAP Objects)"/>` +
	`</tm:task>` +
	`</tm:request>` +
	`</tm:modifiable>` +
	`</tm:customizing>` +
	`</tm:root>`

// s4SingleRequestXML is S/4's single-request shape as captured for the real
// transport S4DK904438 ("dummy transportschicht", modifiable, workbench
// type K). Administrative atom:link entries (consistencycheck,
// sortandcompress, changeowner, reassign, ...) were trimmed for size; the
// request/task-level self+modify+addobject links and — crucially — every
// per-object atom:link (removeobject/lockobject/moveobjects) are kept
// verbatim. Note the real object list under <tm:request> is wrapped in
// <tm:all_objects> (not bare <tm:abap_object> children) — xmlRequest binds
// this wrapper explicitly (see xmlObjectGroup) precisely because a plain
// "abap_object as a direct child of request" binding does not match it.
const s4SingleRequestXML = `<?xml version="1.0" encoding="utf-8"?>` +
	`<tm:root tm:object_type="R" adtcore:responsible="TESTUSER3" adtcore:name="S4DK904438" adtcore:type="RQRQ" adtcore:changedAt="2026-09-11T16:31:58Z" adtcore:changedBy="TESTUSER3" adtcore:description="dummy transportschicht" xmlns:tm="http://www.sap.com/cts/adt/tm" xmlns:adtcore="http://www.sap.com/adt/core">` +
	`<atom:link href="/sap/bc/adt/cts/transportrequests/S4DK904438" rel="http://www.sap.com/cts/relations/adturi" type="application/vnd.sap.adt.transportrequests.v1+xml" title="Transport Organizer ADT URI" xmlns:atom="http://www.w3.org/2005/Atom"/>` +
	`<tm:request tm:number="S4DK904438" tm:parent="" tm:owner="TESTUSER3" tm:desc="dummy transportschicht" tm:type="K" tm:status="D" tm:status_text="Modifiable" tm:target="" tm:target_desc="No target system" tm:cts_project="" tm:cts_project_desc="" tm:source_client="100" tm:lastchanged_timestamp="20260911163158" tm:uri="/sap/bc/adt/cts/transportrequests/S4DK904438">` +
	`<tm:long_desc/>` +
	`<atom:link href="/sap/bc/adt/cts/transportrequests/S4DK904438" rel="http://www.sap.com/cts/relations/modify" type="application/xml" title="Transport Organizer Request/Modify" xmlns:atom="http://www.w3.org/2005/Atom"/>` +
	`<atom:link href="/sap/bc/adt/cts/transportrequests/S4DK904438" rel="http://www.sap.com/cts/relations/addobject" type="application/xml" title="Transport Request/Task Add Objects" xmlns:atom="http://www.w3.org/2005/Atom"/>` +
	`<tm:all_objects>` +
	`<tm:abap_object tm:pgmid="R3TR" tm:type="DEVC" tm:name="Z_TESTPKG" tm:wbtype="DEVC/K" tm:dummy_uri="/sap/bc/adt/cts/transportrequests/reference?obj_name=Z_TESTPKG&amp;obj_wbtype=DEVC&amp;pgmid=R3TR" tm:obj_info="Package" tm:obj_desc="Test package (fixture data)" tm:position="000001" tm:lock_status="X" tm:img_activity="">` +
	`<atom:link href="/sap/bc/adt/cts/transportrequests/S4DK904439" rel="http://www.sap.com/cts/relations/removeobject" type="application/xml" title="Transport Organizer Remove Locked Object" xmlns:atom="http://www.w3.org/2005/Atom"/>` +
	`<atom:link href="/sap/bc/adt/cts/transportrequests/S4DK904439/lockobject" rel="http://www.sap.com/cts/relations/lockobject" type="application/xml" title="Transport Request Editor Lock Objects" xmlns:atom="http://www.w3.org/2005/Atom"/>` +
	`<atom:link href="/sap/bc/adt/cts/transportrequests/S4DK904439/moveobjects" rel="http://www.sap.com/cts/relations/moveobjects" type="application/xml" title="Transport Organizer Move Objects" xmlns:atom="http://www.w3.org/2005/Atom"/>` +
	`</tm:abap_object>` +
	`</tm:all_objects>` +
	`<tm:task tm:number="S4DK904439" tm:parent="S4DK904438" tm:owner="TESTUSER3" tm:desc="dummy transportschicht" tm:type="Development/Correction" tm:status="D" tm:status_text="Modifiable" tm:target="" tm:target_desc="" tm:cts_project="" tm:cts_project_desc="" tm:source_client="100" tm:lastchanged_timestamp="20260911163202" tm:uri="/sap/bc/adt/cts/transportrequests/S4DK904439">` +
	`<tm:long_desc/>` +
	`<atom:link href="/sap/bc/adt/cts/transportrequests/S4DK904439" rel="http://www.sap.com/cts/relations/modify" type="application/xml" title="Transport Organizer Request/Modify" xmlns:atom="http://www.w3.org/2005/Atom"/>` +
	`<atom:link href="/sap/bc/adt/cts/transportrequests/S4DK904439" rel="http://www.sap.com/cts/relations/addobject" type="application/xml" title="Transport Request/Task Add Objects" xmlns:atom="http://www.w3.org/2005/Atom"/>` +
	`<tm:abap_object tm:pgmid="R3TR" tm:type="DEVC" tm:name="Z_TESTPKG" tm:wbtype="DEVC/K" tm:dummy_uri="/sap/bc/adt/cts/transportrequests/reference?obj_name=Z_TESTPKG&amp;obj_wbtype=DEVC&amp;pgmid=R3TR" tm:obj_info="Package" tm:obj_desc="Test package (fixture data)" tm:position="000001" tm:lock_status="X" tm:img_activity="">` +
	`<atom:link href="/sap/bc/adt/cts/transportrequests/S4DK904439" rel="http://www.sap.com/cts/relations/removeobject" type="application/xml" title="Transport Organizer Remove Locked Object" xmlns:atom="http://www.w3.org/2005/Atom"/>` +
	`<atom:link href="/sap/bc/adt/cts/transportrequests/S4DK904439/lockobject" rel="http://www.sap.com/cts/relations/lockobject" type="application/xml" title="Transport Request Editor Lock Objects" xmlns:atom="http://www.w3.org/2005/Atom"/>` +
	`<atom:link href="/sap/bc/adt/cts/transportrequests/S4DK904439/moveobjects" rel="http://www.sap.com/cts/relations/moveobjects" type="application/xml" title="Transport Organizer Move Objects" xmlns:atom="http://www.w3.org/2005/Atom"/>` +
	`</tm:abap_object>` +
	`</tm:task>` +
	`</tm:request>` +
	`</tm:root>`

// s4RequestNoObjectsXML is S/4's single-request shape for a real, genuinely
// empty modifiable request (S4DK904476, a shared fixture transport used by
// this repo's own integration tests — "MCP integration test fixtures").
// There is no <tm:all_objects> element at all when a request holds no
// objects (the wrapper is omitted, not present-but-empty) — confirmed on
// the wire, not assumed. Task 5's capability rule is defined against this;
// getting it wrong makes the gate block a system that supports removal.
const s4RequestNoObjectsXML = `<?xml version="1.0" encoding="utf-8"?>` +
	`<tm:root tm:object_type="R" adtcore:responsible="TESTUSER1" adtcore:name="S4DK904476" adtcore:type="RQRQ" adtcore:changedAt="2026-09-16T12:53:09Z" adtcore:changedBy="TESTUSER1" adtcore:description="MCP integration test fixtures" xmlns:tm="http://www.sap.com/cts/adt/tm" xmlns:adtcore="http://www.sap.com/adt/core">` +
	`<atom:link href="/sap/bc/adt/cts/transportrequests/S4DK904476" rel="http://www.sap.com/cts/relations/adturi" type="application/vnd.sap.adt.transportrequests.v1+xml" title="Transport Organizer ADT URI" xmlns:atom="http://www.w3.org/2005/Atom"/>` +
	`<tm:request tm:number="S4DK904476" tm:parent="" tm:owner="TESTUSER1" tm:desc="MCP integration test fixtures" tm:type="K" tm:status="D" tm:status_text="Modifiable" tm:target="DUM" tm:target_desc="" tm:cts_project="" tm:cts_project_desc="" tm:source_client="100" tm:lastchanged_timestamp="20260916125309" tm:uri="/sap/bc/adt/cts/transportrequests/S4DK904476">` +
	`<tm:long_desc/>` +
	`<atom:link href="/sap/bc/adt/cts/transportrequests/S4DK904476" rel="http://www.sap.com/cts/relations/modify" type="application/xml" title="Transport Organizer Request/Modify" xmlns:atom="http://www.w3.org/2005/Atom"/>` +
	`<atom:link href="/sap/bc/adt/cts/transportrequests/S4DK904476" rel="http://www.sap.com/cts/relations/addobject" type="application/xml" title="Transport Request/Task Add Objects" xmlns:atom="http://www.w3.org/2005/Atom"/>` +
	`<atom:link href="/sap/bc/adt/cts/transportrequests/S4DK904476/tasks" rel="http://www.sap.com/cts/relations/newtask" type="application/xml" title="Transport Organizer New Task Creation" xmlns:atom="http://www.w3.org/2005/Atom"/>` +
	`<tm:task tm:number="S4DK904477" tm:parent="S4DK904476" tm:owner="TESTUSER1" tm:desc="MCP integration test fixtures" tm:type="Unclassified" tm:status="D" tm:status_text="Modifiable" tm:target="" tm:target_desc="" tm:cts_project="" tm:cts_project_desc="" tm:source_client="100" tm:lastchanged_timestamp="20260916125309" tm:uri="/sap/bc/adt/cts/transportrequests/S4DK904477">` +
	`<tm:long_desc/>` +
	`<atom:link href="/sap/bc/adt/cts/transportrequests/S4DK904477" rel="http://www.sap.com/cts/relations/modify" type="application/xml" title="Transport Organizer Request/Modify" xmlns:atom="http://www.w3.org/2005/Atom"/>` +
	`<atom:link href="/sap/bc/adt/cts/transportrequests/S4DK904477" rel="http://www.sap.com/cts/relations/addobject" type="application/xml" title="Transport Request/Task Add Objects" xmlns:atom="http://www.w3.org/2005/Atom"/>` +
	`</tm:task>` +
	`</tm:request>` +
	`</tm:root>`

// s4ObjectAtBothLevelsXML is hand-built — no live S/4 response was found with
// this shape — from s4SingleRequestXML by adding a bare <tm:abap_object> for
// the same object (R3TR/DEVC/Z_TESTPKG) directly as a child of <tm:request>,
// alongside the identical object already present inside <tm:task>. The
// object-dedup rule in parseTransportObjectsXML is defined against it.
const s4ObjectAtBothLevelsXML = `<?xml version="1.0" encoding="utf-8"?>` +
	`<tm:root tm:object_type="R" adtcore:responsible="TESTUSER3" adtcore:name="S4DK904438" adtcore:type="RQRQ" adtcore:changedAt="2026-09-11T16:31:58Z" adtcore:changedBy="TESTUSER3" adtcore:description="dummy transportschicht" xmlns:tm="http://www.sap.com/cts/adt/tm" xmlns:adtcore="http://www.sap.com/adt/core">` +
	`<atom:link href="/sap/bc/adt/cts/transportrequests/S4DK904438" rel="http://www.sap.com/cts/relations/adturi" type="application/vnd.sap.adt.transportrequests.v1+xml" title="Transport Organizer ADT URI" xmlns:atom="http://www.w3.org/2005/Atom"/>` +
	`<tm:request tm:number="S4DK904438" tm:parent="" tm:owner="TESTUSER3" tm:desc="dummy transportschicht" tm:type="K" tm:status="D" tm:status_text="Modifiable" tm:target="" tm:target_desc="No target system" tm:cts_project="" tm:cts_project_desc="" tm:source_client="100" tm:lastchanged_timestamp="20260911163158" tm:uri="/sap/bc/adt/cts/transportrequests/S4DK904438">` +
	`<tm:long_desc/>` +
	`<atom:link href="/sap/bc/adt/cts/transportrequests/S4DK904438" rel="http://www.sap.com/cts/relations/modify" type="application/xml" title="Transport Organizer Request/Modify" xmlns:atom="http://www.w3.org/2005/Atom"/>` +
	// Hand-added: the same object as a bare, direct child of <tm:request>
	// (outside tm:all_objects), duplicating the one inside <tm:task> below.
	`<tm:abap_object tm:pgmid="R3TR" tm:type="DEVC" tm:name="Z_TESTPKG" tm:wbtype="DEVC/K" tm:dummy_uri="/sap/bc/adt/cts/transportrequests/reference?obj_name=Z_TESTPKG&amp;obj_wbtype=DEVC&amp;pgmid=R3TR" tm:obj_info="Package" tm:obj_desc="Test package (fixture data)" tm:position="000001" tm:lock_status="X" tm:img_activity="">` +
	`<atom:link href="/sap/bc/adt/cts/transportrequests/S4DK904439" rel="http://www.sap.com/cts/relations/removeobject" type="application/xml" title="Transport Organizer Remove Locked Object" xmlns:atom="http://www.w3.org/2005/Atom"/>` +
	`</tm:abap_object>` +
	`<tm:all_objects>` +
	`<tm:abap_object tm:pgmid="R3TR" tm:type="DEVC" tm:name="Z_TESTPKG" tm:wbtype="DEVC/K" tm:dummy_uri="/sap/bc/adt/cts/transportrequests/reference?obj_name=Z_TESTPKG&amp;obj_wbtype=DEVC&amp;pgmid=R3TR" tm:obj_info="Package" tm:obj_desc="Test package (fixture data)" tm:position="000001" tm:lock_status="X" tm:img_activity="">` +
	`<atom:link href="/sap/bc/adt/cts/transportrequests/S4DK904439" rel="http://www.sap.com/cts/relations/removeobject" type="application/xml" title="Transport Organizer Remove Locked Object" xmlns:atom="http://www.w3.org/2005/Atom"/>` +
	`</tm:abap_object>` +
	`</tm:all_objects>` +
	`<tm:task tm:number="S4DK904439" tm:parent="S4DK904438" tm:owner="TESTUSER3" tm:desc="dummy transportschicht" tm:type="Development/Correction" tm:status="D" tm:status_text="Modifiable" tm:target="" tm:target_desc="" tm:cts_project="" tm:cts_project_desc="" tm:source_client="100" tm:lastchanged_timestamp="20260911163202" tm:uri="/sap/bc/adt/cts/transportrequests/S4DK904439">` +
	`<tm:long_desc/>` +
	`<atom:link href="/sap/bc/adt/cts/transportrequests/S4DK904439" rel="http://www.sap.com/cts/relations/modify" type="application/xml" title="Transport Organizer Request/Modify" xmlns:atom="http://www.w3.org/2005/Atom"/>` +
	`<tm:abap_object tm:pgmid="R3TR" tm:type="DEVC" tm:name="Z_TESTPKG" tm:wbtype="DEVC/K" tm:dummy_uri="/sap/bc/adt/cts/transportrequests/reference?obj_name=Z_TESTPKG&amp;obj_wbtype=DEVC&amp;pgmid=R3TR" tm:obj_info="Package" tm:obj_desc="Test package (fixture data)" tm:position="000001" tm:lock_status="X" tm:img_activity="">` +
	`<atom:link href="/sap/bc/adt/cts/transportrequests/S4DK904439" rel="http://www.sap.com/cts/relations/removeobject" type="application/xml" title="Transport Organizer Remove Locked Object" xmlns:atom="http://www.w3.org/2005/Atom"/>` +
	`<atom:link href="/sap/bc/adt/cts/transportrequests/S4DK904439/lockobject" rel="http://www.sap.com/cts/relations/lockobject" type="application/xml" title="Transport Request Editor Lock Objects" xmlns:atom="http://www.w3.org/2005/Atom"/>` +
	`<atom:link href="/sap/bc/adt/cts/transportrequests/S4DK904439/moveobjects" rel="http://www.sap.com/cts/relations/moveobjects" type="application/xml" title="Transport Organizer Move Objects" xmlns:atom="http://www.w3.org/2005/Atom"/>` +
	`</tm:abap_object>` +
	`</tm:task>` +
	`</tm:request>` +
	`</tm:root>`

// s4ObjectDivergentPositionAndTaskXML is hand-built, from a round of review
// feedback on the dedup tests below, from s4ObjectAtBothLevelsXML's shape.
// In every other fixture in this file
// that duplicates an object across levels, every occurrence carries the
// identical tm:position (and, where a task is involved, the same single
// task), so a test asserting "kept the first-seen value" cannot actually
// distinguish that from "last occurrence overwrote the whole entry" or "last
// task wins" — the values just happen to agree either way. This fixture
// breaks that coincidence on purpose: the bare, direct-child-of-<tm:request>
// occurrence of R3TR/DEVC/Z_TESTPKG carries position 000001 and no task; it
// recurs under two different tasks (S4DK904439 at position 000002, then the
// hand-added S4DK904440 at position 000003). A correct parse keeps position
// 000001 and attributes the object to S4DK904439 — the first task to record
// it — never to S4DK904440.
const s4ObjectDivergentPositionAndTaskXML = `<?xml version="1.0" encoding="utf-8"?>` +
	`<tm:root tm:object_type="R" adtcore:responsible="TESTUSER3" adtcore:name="S4DK904438" adtcore:type="RQRQ" adtcore:changedAt="2026-09-11T16:31:58Z" adtcore:changedBy="TESTUSER3" adtcore:description="dummy transportschicht" xmlns:tm="http://www.sap.com/cts/adt/tm" xmlns:adtcore="http://www.sap.com/adt/core">` +
	`<atom:link href="/sap/bc/adt/cts/transportrequests/S4DK904438" rel="http://www.sap.com/cts/relations/adturi" type="application/vnd.sap.adt.transportrequests.v1+xml" title="Transport Organizer ADT URI" xmlns:atom="http://www.w3.org/2005/Atom"/>` +
	`<tm:request tm:number="S4DK904438" tm:parent="" tm:owner="TESTUSER3" tm:desc="dummy transportschicht" tm:type="K" tm:status="D" tm:status_text="Modifiable" tm:target="" tm:target_desc="No target system" tm:cts_project="" tm:cts_project_desc="" tm:source_client="100" tm:lastchanged_timestamp="20260911163158" tm:uri="/sap/bc/adt/cts/transportrequests/S4DK904438">` +
	`<tm:long_desc/>` +
	`<atom:link href="/sap/bc/adt/cts/transportrequests/S4DK904438" rel="http://www.sap.com/cts/relations/modify" type="application/xml" title="Transport Organizer Request/Modify" xmlns:atom="http://www.w3.org/2005/Atom"/>` +
	// Bare, direct child of <tm:request>: the first-seen occurrence — position
	// 000001, no task.
	`<tm:abap_object tm:pgmid="R3TR" tm:type="DEVC" tm:name="Z_TESTPKG" tm:wbtype="DEVC/K" tm:dummy_uri="/sap/bc/adt/cts/transportrequests/reference?obj_name=Z_TESTPKG&amp;obj_wbtype=DEVC&amp;pgmid=R3TR" tm:obj_info="Package" tm:obj_desc="Test package (fixture data)" tm:position="000001" tm:lock_status="X" tm:img_activity="">` +
	`<atom:link href="/sap/bc/adt/cts/transportrequests/S4DK904439" rel="http://www.sap.com/cts/relations/removeobject" type="application/xml" title="Transport Organizer Remove Locked Object" xmlns:atom="http://www.w3.org/2005/Atom"/>` +
	`</tm:abap_object>` +
	// First task: the same object again, at a *different* position (000002)
	// from the request-level occurrence above.
	`<tm:task tm:number="S4DK904439" tm:parent="S4DK904438" tm:owner="TESTUSER3" tm:desc="dummy transportschicht" tm:type="Development/Correction" tm:status="D" tm:status_text="Modifiable" tm:target="" tm:target_desc="" tm:cts_project="" tm:cts_project_desc="" tm:source_client="100" tm:lastchanged_timestamp="20260911163202" tm:uri="/sap/bc/adt/cts/transportrequests/S4DK904439">` +
	`<tm:long_desc/>` +
	`<atom:link href="/sap/bc/adt/cts/transportrequests/S4DK904439" rel="http://www.sap.com/cts/relations/modify" type="application/xml" title="Transport Organizer Request/Modify" xmlns:atom="http://www.w3.org/2005/Atom"/>` +
	`<tm:abap_object tm:pgmid="R3TR" tm:type="DEVC" tm:name="Z_TESTPKG" tm:wbtype="DEVC/K" tm:dummy_uri="/sap/bc/adt/cts/transportrequests/reference?obj_name=Z_TESTPKG&amp;obj_wbtype=DEVC&amp;pgmid=R3TR" tm:obj_info="Package" tm:obj_desc="Test package (fixture data)" tm:position="000002" tm:lock_status="X" tm:img_activity="">` +
	`<atom:link href="/sap/bc/adt/cts/transportrequests/S4DK904439" rel="http://www.sap.com/cts/relations/removeobject" type="application/xml" title="Transport Organizer Remove Locked Object" xmlns:atom="http://www.w3.org/2005/Atom"/>` +
	`</tm:abap_object>` +
	`</tm:task>` +
	// Second task: hand-added. The same object once more, under a different
	// task number (S4DK904440) and yet another position (000003) — no other
	// fixture in this file attributes one object to two different tasks.
	`<tm:task tm:number="S4DK904440" tm:parent="S4DK904438" tm:owner="TESTUSER3" tm:desc="dummy transportschicht" tm:type="Development/Correction" tm:status="D" tm:status_text="Modifiable" tm:target="" tm:target_desc="" tm:cts_project="" tm:cts_project_desc="" tm:source_client="100" tm:lastchanged_timestamp="20260911163203" tm:uri="/sap/bc/adt/cts/transportrequests/S4DK904440">` +
	`<tm:long_desc/>` +
	`<atom:link href="/sap/bc/adt/cts/transportrequests/S4DK904440" rel="http://www.sap.com/cts/relations/modify" type="application/xml" title="Transport Organizer Request/Modify" xmlns:atom="http://www.w3.org/2005/Atom"/>` +
	`<tm:abap_object tm:pgmid="R3TR" tm:type="DEVC" tm:name="Z_TESTPKG" tm:wbtype="DEVC/K" tm:dummy_uri="/sap/bc/adt/cts/transportrequests/reference?obj_name=Z_TESTPKG&amp;obj_wbtype=DEVC&amp;pgmid=R3TR" tm:obj_info="Package" tm:obj_desc="Test package (fixture data)" tm:position="000003" tm:lock_status="X" tm:img_activity="">` +
	`<atom:link href="/sap/bc/adt/cts/transportrequests/S4DK904440" rel="http://www.sap.com/cts/relations/removeobject" type="application/xml" title="Transport Organizer Remove Locked Object" xmlns:atom="http://www.w3.org/2005/Atom"/>` +
	`</tm:abap_object>` +
	`</tm:task>` +
	`</tm:request>` +
	`</tm:root>`

// assertWellFormed fails the test if data does not parse as XML.
func assertWellFormed(t *testing.T, name, data string) {
	t.Helper()
	var v any
	if err := xml.Unmarshal([]byte(data), &v); err != nil {
		t.Errorf("%s: not well-formed XML: %v", name, err)
	}
}

func TestTransportECCFixtures_WellFormed(t *testing.T) {
	fixtures := map[string]string{
		"eccWorklistXML":                      eccWorklistXML,
		"eccWorklistEmptyNumberXML":           eccWorklistEmptyNumberXML,
		"eccCustomizingXML":                   eccCustomizingXML,
		"s4SingleRequestXML":                  s4SingleRequestXML,
		"s4RequestNoObjectsXML":               s4RequestNoObjectsXML,
		"s4ObjectAtBothLevelsXML":             s4ObjectAtBothLevelsXML,
		"s4ObjectDivergentPositionAndTaskXML": s4ObjectDivergentPositionAndTaskXML,
	}
	for name, data := range fixtures {
		t.Run(name, func(t *testing.T) {
			assertWellFormed(t, name, data)
		})
	}
}

// TestEccWorklistXML_StructuralProperties pins down the properties Task 2
// relies on: at least two distinct request numbers, and no position
// attribute anywhere (ECC's worklist shape never carries one).
func TestEccWorklistXML_StructuralProperties(t *testing.T) {
	if strings.Contains(eccWorklistXML, "position=") {
		t.Error("eccWorklistXML must not contain a position attribute")
	}
	for _, number := range []string{`tm:number="DEVK900178"`, `tm:number="DEVK902952"`} {
		if !strings.Contains(eccWorklistXML, number) {
			t.Errorf("eccWorklistXML missing expected request %s", number)
		}
	}
	if strings.Count(eccWorklistXML, `<tm:request `) < 2 {
		t.Fatalf("eccWorklistXML must contain at least two <tm:request> elements")
	}
}

// TestEccWorklistEmptyNumberXML_HasEmptyNumberRequestWithObjects verifies the
// hand-edited variant still holds a task/object under the blanked-number request.
func TestEccWorklistEmptyNumberXML_HasEmptyNumberRequestWithObjects(t *testing.T) {
	if !strings.Contains(eccWorklistEmptyNumberXML, `tm:request tm:number=""`) {
		t.Fatal(`eccWorklistEmptyNumberXML must contain tm:request tm:number=""`)
	}
	if !strings.Contains(eccWorklistEmptyNumberXML, "ZCL_LOCKREPRO_2") {
		t.Error("eccWorklistEmptyNumberXML must still hold the object under the empty-number request")
	}
}

// TestEccCustomizingXML_RequestUnderCustomizingGroup verifies the
// hand-edited variant places a request (with its object) under the
// customizing group rather than workbench.
func TestEccCustomizingXML_RequestUnderCustomizingGroup(t *testing.T) {
	custIdx := strings.Index(eccCustomizingXML, `<tm:customizing tm:category="Customizing">`)
	if custIdx < 0 {
		t.Fatal("eccCustomizingXML missing <tm:customizing> group")
	}
	if !strings.Contains(eccCustomizingXML[custIdx:], `tm:number="DEVK902952"`) {
		t.Error("eccCustomizingXML: DEVK902952 must sit under the customizing group")
	}
	if !strings.Contains(eccCustomizingXML[custIdx:], "ZCL_LOCKREPRO_2") {
		t.Error("eccCustomizingXML: object must be present under the customizing request")
	}
}

// TestRemoveObjectRelation_S4OnlyNotECC pins the acceptance criterion that
// s4SingleRequestXML carries the removeobject relation and eccWorklistXML
// does not: the captured ECC worklist body never emits per-object atom
// relations at all (its only atom:link elements sit on requests/tasks, and
// list only consistencycheck/releasejobs/modify/newtask).
func TestRemoveObjectRelation_S4OnlyNotECC(t *testing.T) {
	if !strings.Contains(s4SingleRequestXML, "removeobject") {
		t.Error("s4SingleRequestXML must contain the removeobject relation")
	}
	if strings.Contains(eccWorklistXML, "removeobject") {
		t.Error("eccWorklistXML must not contain the removeobject relation")
	}
}

// TestS4RequestNoObjectsXML_NoAbapObject pins the acceptance criterion that
// this fixture holds no abap_object at all.
func TestS4RequestNoObjectsXML_NoAbapObject(t *testing.T) {
	if strings.Contains(s4RequestNoObjectsXML, "abap_object") {
		t.Error("s4RequestNoObjectsXML must contain no abap_object element")
	}
	if !strings.Contains(s4RequestNoObjectsXML, `tm:number="S4DK904476"`) {
		t.Error("s4RequestNoObjectsXML must still carry the request-level atom links/number")
	}
}

// TestS4ObjectAtBothLevelsXML_SameObjectBothLevels pins the acceptance
// criterion that the same object (pgmid/type/name) appears both directly
// under <tm:request> and under a <tm:task>.
func TestS4ObjectAtBothLevelsXML_SameObjectBothLevels(t *testing.T) {
	const objectAttrs = `tm:pgmid="R3TR" tm:type="DEVC" tm:name="Z_TESTPKG"`
	count := strings.Count(s4ObjectAtBothLevelsXML, objectAttrs)
	if count < 3 { // direct-under-request + all_objects + under-task
		t.Fatalf("expected the object to appear at least 3 times (direct, all_objects, task), got %d", count)
	}

	reqIdx := strings.Index(s4ObjectAtBothLevelsXML, "<tm:request ")
	taskIdx := strings.Index(s4ObjectAtBothLevelsXML, "<tm:task ")
	allObjectsIdx := strings.Index(s4ObjectAtBothLevelsXML, "<tm:all_objects>")
	if reqIdx < 0 || taskIdx < 0 || allObjectsIdx < 0 {
		t.Fatal("s4ObjectAtBothLevelsXML missing request/task/all_objects structure")
	}
	directSlice := s4ObjectAtBothLevelsXML[reqIdx:allObjectsIdx]
	if !strings.Contains(directSlice, objectAttrs) {
		t.Error("s4ObjectAtBothLevelsXML: object must appear as a bare direct child of <tm:request>")
	}
	taskSlice := s4ObjectAtBothLevelsXML[taskIdx:]
	if !strings.Contains(taskSlice, objectAttrs) {
		t.Error("s4ObjectAtBothLevelsXML: object must appear under <tm:task>")
	}
}
