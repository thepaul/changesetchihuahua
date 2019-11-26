package dbx

//go:generate dbx.v1 golang -p dbx -d postgres -d sqlite3 usermap.dbx .

func init() {
	WrapErr = wrapDBXErr
}

func wrapDBXErr(e *Error) error {
	switch e.Code {
	case ErrorCode_NoRows:
		return e.Err
	}
	return e
}
