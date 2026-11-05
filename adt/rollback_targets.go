package adt

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// rollbackTarget is one unit of source RollbackTransportWithOptions restores:
// an object with its own version history and its own ADT URI. Several
// transport entries can resolve to the same target (a class whose methods
// were transported one by one), which is restored once.
type rollbackTarget struct {
	// entry is what the result reports: the TADIR object (PROG, CLAS, INTF,
	// FUGR) where there is one, otherwise the sub-object itself (FUNC for a
	// function module, REPS for a function group include).
	entry RollbackEntry
	uri   string
	// tadir is true when entry is a TADIR (R3TR) object. Only such an object
	// is deleted when the transport created it; a function module or a
	// function group include is never deleted by a rollback.
	tadir bool
}

// pgmIDCorr marks E071 rows such as CORR RELE: release markers whose OBJ_NAME
// is an audit string, not an object name.
const pgmIDCorr = "CORR"

// pgmIDLIMU marks an E071 row for part of an object (a method, a program
// include, a function module).
const pgmIDLIMU = "LIMU"

// limuClassParts are the sub-object types that SAP records for a change to a
// class's global source (method implementation, public / private / protected
// section, class definition). ADT versions all of them under the class's
// "main" include, so they resolve to the class.
var limuClassParts = map[string]bool{
	"METH": true, "CPUB": true, "CPRI": true, "CPRO": true, "CLSD": true,
}

// Reasons recorded on RollbackResult.Skipped entries.
const (
	rollbackReasonNonSourceType  = "non-source object type"
	rollbackReasonNonSourceLIMU  = "sub-object type without source this rollback restores (screen, GUI status, text pool, ...)"
	rollbackReasonUnresolvedLIMU = "sub-object could not be mapped to the object that carries its source"
)

// classPoolNameLen is the length to which SAP pads a class or interface name
// with "=" when it names one of the pool's includes (ZCL_X====...CCIMP).
const classPoolNameLen = 30

// resolveRollbackTarget maps one transport entry to the source unit a
// rollback restores. It returns ok == false with a skip reason for an entry
// that has no such unit, and an error when the lookup itself failed; a 401
// among those errors ends the rollback (#222).
//
// R3TR entries of rollbackSourceTypes are their own unit. A LIMU entry is
// what SAP records when only part of an object changed, which is what a
// released workbench request normally contains (#134):
//
//   - METH, CPUB, CPRI, CPRO, CLSD: the class, from the first token of the
//     padded OBJ_NAME ("ZCL_X   METHOD_A" → ZCL_X).
//   - CINC, and REPS of a class or interface pool include
//     ("ZCL_X=====CCIMP"): the class or interface.
//   - REPS of a program: by TRDIR, the main program (R3TR PROG: an
//     executable, module pool or subroutine pool), the
//     function pool SAPL<group> (R3TR FUGR <group>), a standalone include
//     (R3TR PROG, written through /programs/includes) or a function group
//     include L<group>... (written through the group's includes).
//   - FUNC: the function module, in the group TFDIR names.
//
// Everything else (DYNP, CUAD, REPT, ...) has no source to restore.
func (c *httpClient) resolveRollbackTarget(ctx context.Context, obj TransportObject) (rollbackTarget, string, bool, error) {
	switch obj.PgmID {
	case "R3TR":
		if !rollbackSourceTypes[obj.Type] {
			return rollbackTarget{}, rollbackReasonNonSourceType, false, nil
		}
		return r3trTarget(obj.Type, obj.Name)
	case pgmIDLIMU:
		return c.resolveLIMU(ctx, obj)
	default:
		return rollbackTarget{}, "not R3TR or LIMU", false, nil
	}
}

func (c *httpClient) resolveLIMU(ctx context.Context, obj TransportObject) (rollbackTarget, string, bool, error) {
	name := strings.TrimSpace(obj.Name)
	switch {
	case limuClassParts[obj.Type]:
		fields := strings.Fields(name)
		if len(fields) == 0 {
			return rollbackTarget{}, rollbackReasonUnresolvedLIMU, false, nil
		}
		return r3trTarget(objTypeCLAS, fields[0])
	case obj.Type == "CINC":
		if t, ok := poolIncludeTarget(name, false); ok {
			return t, "", true, nil
		}
		return rollbackTarget{}, rollbackReasonUnresolvedLIMU, false, nil
	case obj.Type == "REPS":
		return c.resolveREPS(ctx, name)
	case obj.Type == "FUNC":
		return c.resolveFUNC(ctx, name)
	default:
		return rollbackTarget{}, rollbackReasonNonSourceLIMU, false, nil
	}
}

// poolIncludeTarget resolves an include of a class pool (suffix C...) or an
// interface pool (suffix I...) to the class or interface. With needPadding,
// the owner's name must be "="-padded, which tells a pool include apart from
// an ordinary program name longer than 30 characters; it costs the pool
// includes of a class whose name has exactly 30 characters, which then stay
// unresolved.
func poolIncludeTarget(name string, needPadding bool) (rollbackTarget, bool) {
	if len(name) <= classPoolNameLen {
		return rollbackTarget{}, false
	}
	if needPadding && !strings.HasSuffix(name[:classPoolNameLen], "=") {
		return rollbackTarget{}, false
	}
	owner := strings.TrimRight(name[:classPoolNameLen], "=")
	var objType string
	switch name[classPoolNameLen] {
	case 'C':
		objType = objTypeCLAS
	case 'I':
		objType = objTypeINTF
	default:
		return rollbackTarget{}, false
	}
	t, _, ok, _ := r3trTarget(objType, owner)
	return t, ok
}

func (c *httpClient) resolveREPS(ctx context.Context, name string) (rollbackTarget, string, bool, error) {
	if t, ok := poolIncludeTarget(name, true); ok {
		return t, "", true, nil
	}
	subc, found, err := c.queryOne(ctx, fmt.Sprintf(
		"SELECT SUBC FROM TRDIR WHERE NAME = '%s'", EscapeValue(name)))
	if err != nil || !found {
		return rollbackTarget{}, "no program " + name + " in TRDIR", false, err
	}
	switch subc {
	case "F":
		ns, rest := splitNamespace(name)
		if !strings.HasPrefix(rest, "SAPL") {
			return rollbackTarget{}, rollbackReasonUnresolvedLIMU, false, nil
		}
		return r3trTarget("FUGR", ns+strings.TrimPrefix(rest, "SAPL"))
	case "I":
		return c.resolveInclude(ctx, name)
	case "1", "M", "S": // executable program, module pool, subroutine pool
		return r3trTarget("PROG", name)
	default:
		return rollbackTarget{}, rollbackReasonUnresolvedLIMU, false, nil
	}
}

// resolveInclude resolves the program include name: a standalone include is
// an R3TR PROG of its own; otherwise it is a function group include, named
// L<group> plus a three-character suffix, whose group TADIR must know.
func (c *httpClient) resolveInclude(ctx context.Context, name string) (rollbackTarget, string, bool, error) {
	_, standalone, err := c.queryOne(ctx, fmt.Sprintf(
		"SELECT OBJ_NAME FROM TADIR WHERE PGMID = 'R3TR' AND OBJECT = 'PROG' AND OBJ_NAME = '%s'", EscapeValue(name)))
	if err != nil {
		return rollbackTarget{}, "", false, err
	}
	if standalone {
		return rollbackTarget{
			entry: RollbackEntry{Type: "PROG", Name: name},
			uri:   "/sap/bc/adt/programs/includes/" + adtName(name),
			tadir: true,
		}, "", true, nil
	}
	ns, rest := splitNamespace(name)
	if !strings.HasPrefix(rest, "L") || len(rest) <= 4 {
		return rollbackTarget{}, rollbackReasonUnresolvedLIMU, false, nil
	}
	group := ns + rest[1:len(rest)-3]
	_, isGroup, err := c.queryOne(ctx, fmt.Sprintf(
		"SELECT OBJ_NAME FROM TADIR WHERE PGMID = 'R3TR' AND OBJECT = 'FUGR' AND OBJ_NAME = '%s'", EscapeValue(group)))
	if err != nil || !isGroup {
		return rollbackTarget{}, rollbackReasonUnresolvedLIMU, false, err
	}
	return rollbackTarget{
		entry: RollbackEntry{Type: "REPS", Name: name},
		uri:   "/sap/bc/adt/functions/groups/" + adtName(group) + "/includes/" + adtName(name),
	}, "", true, nil
}

func (c *httpClient) resolveFUNC(ctx context.Context, name string) (rollbackTarget, string, bool, error) {
	pname, found, err := c.queryOne(ctx, fmt.Sprintf(
		"SELECT PNAME FROM TFDIR WHERE FUNCNAME = '%s'", EscapeValue(name)))
	if err != nil || !found {
		return rollbackTarget{}, "no function module " + name + " in TFDIR", false, err
	}
	ns, rest := splitNamespace(pname)
	if !strings.HasPrefix(rest, "SAPL") {
		return rollbackTarget{}, rollbackReasonUnresolvedLIMU, false, nil
	}
	group := ns + strings.TrimPrefix(rest, "SAPL")
	return rollbackTarget{
		entry: RollbackEntry{Type: "FUNC", Name: name},
		uri:   "/sap/bc/adt/functions/groups/" + adtName(group) + "/fmodules/" + adtName(name),
	}, "", true, nil
}

// r3trTarget is the target for the R3TR object objType/name.
func r3trTarget(objType, name string) (rollbackTarget, string, bool, error) {
	uri, err := ObjectURI(objType, "")
	if err != nil {
		return rollbackTarget{}, err.Error(), false, nil
	}
	return rollbackTarget{
		entry: RollbackEntry{Type: objType, Name: name},
		uri:   uri + adtName(name),
		tadir: true,
	}, "", true, nil
}

// adtName is name as an ADT URI path segment: lower case, with a namespace's
// slashes escaped (/ABC/FOO → %2fabc%2ffoo).
func adtName(name string) string {
	return strings.ToLower(url.PathEscape(name))
}

// splitNamespace splits "/ABC/REST" into "/ABC/" and "REST"; a name without
// a namespace comes back as "" and the name.
func splitNamespace(name string) (string, string) {
	if strings.HasPrefix(name, "/") {
		if i := strings.Index(name[1:], "/"); i >= 0 {
			return name[:i+2], name[i+2:]
		}
	}
	return "", name
}

// queryOne runs sql and returns the trimmed first column of its first row.
func (c *httpClient) queryOne(ctx context.Context, sql string) (string, bool, error) {
	qr, err := c.RunQuery(ctx, sql, 1)
	if err != nil {
		return "", false, err
	}
	if len(qr.Rows) == 0 || len(qr.Rows[0]) == 0 {
		return "", false, nil
	}
	return strings.TrimSpace(qr.Rows[0][0]), true, nil
}
