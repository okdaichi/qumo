package rtmp

func OpenFLVStream(conn *Conn) (*Stream, error) {
	// TODO: Implement FLV media stream handling
	stream, err := conn.OpenStream()
	if err != nil {
		// Handle error appropriately
		return nil, err
	}
	return stream, nil
}
