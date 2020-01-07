package dbx

//go:generate dbx.v1 golang -p dbx -d postgres -d sqlite3 persistent.dbx .

func init() {
	WrapErr = wrapDBXErr
}

func wrapDBXErr(e *Error) error {
	if e.Code == ErrorCode_NoRows {
		return e.Err
	}
	return e
}
