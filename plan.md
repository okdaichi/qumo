1. *Refactor `TestListener_Listen` into `TestListen` in `internal/rtmp/listener_test.go`.*
   - Group the tests into subtests: `success` and `error`, following the pattern in `TestDial`.
   - In the `success` subtest, actually dial into the local listener created by `Listen` and verify that `Accept()` works and wraps the connection correctly.
   - Move the existing `TestListen_Error` logic into the `error` subtest.
2. *Run the tests to verify the changes.*
   - Execute `go test -v ./internal/rtmp -run TestListen` to ensure the new subtests pass.
3. *Complete pre-commit steps.*
   - Complete pre-commit steps to make sure proper testing, verifications, reviews, and reflections are done.
4. *Submit the change.*
   - Once everything passes, submit the change with a descriptive commit message.
