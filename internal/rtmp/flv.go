package rtmp

// OpenFLVStream opens a client-side media stream for FLV data.
func OpenFLVStream(conn *Conn, app, streamKey string) (*MessageWriter, error) {
	return conn.OpenStream(app, streamKey)
}
