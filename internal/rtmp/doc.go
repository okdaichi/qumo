// Package rtmp implements the RTMP (Real-Time Messaging Protocol) v1.0
// specification for live media ingest.
//
// # Server
//
// A server listens for incoming RTMP connections and accepts publish streams:
//
//	ln, err := rtmp.Listen("tcp", ":1935")
//	if err != nil {
//		log.Fatal(err)
//	}
//	defer ln.Close()
//
//	conn, err := ln.Accept()
//	if err != nil {
//		log.Fatal(err)
//	}
//	defer conn.Close()
//
//	reader, err := conn.AcceptStream()
//	if err != nil {
//		log.Fatal(err)
//	}
//	defer reader.Close()
//
//	log.Printf("app=%s key=%s", reader.App(), reader.StreamKey())
//
//	for {
//		frame, err := reader.ReadFrame()
//		if err != nil {
//			break
//		}
//		log.Printf("%s ts=%d len=%d", frame.Type, frame.Timestamp, len(frame.Data))
//	}
//
// # Client
//
// A client connects to an RTMP server and publishes frames:
//
//	conn, err := rtmp.Dial(ctx, "localhost:1935")
//	if err != nil {
//		log.Fatal(err)
//	}
//
//	writer, err := conn.OpenStream("live", "stream-key")
//	if err != nil {
//		log.Fatal(err)
//	}
//	defer writer.Close()
//
//	err = writer.WriteFrame(&rtmp.Frame{
//		Type:      rtmp.FrameTypeVideo,
//		Timestamp: 0,
//		Data:      videoPayload,
//	})
//
// # Low-level connection
//
// For applications that manage their own TCP connections, [ServerConn] and
// [ClientConn] perform the RTMP handshake on an existing [net.Conn]:
//
//	serverConn, err := rtmp.ServerConn(tcpConn) // server side
//	clientConn, err := rtmp.ClientConn(tcpConn) // client side
package rtmp
