package dbtypes

type DBEngineType int

const (
	DBEngineAny    DBEngineType = 0
	DBEngineSqlite DBEngineType = 1
	DBEnginePgsql  DBEngineType = 2
)
