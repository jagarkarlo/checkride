package schema

import _ "embed"

// DrillRunJSONSchema is the Studio/API contract for recorded drill evidence.
//
//go:embed drillrun.schema.json
var DrillRunJSONSchema []byte
