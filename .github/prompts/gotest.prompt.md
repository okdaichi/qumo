# Go Test Code Generation Rules

This prompt defines the rules and patterns to follow when generating test code for Go projects.

## Core Principles

### 1. Test Function Naming Conventions
- **Basic Tests**: `Test[FunctionName]`
- **Method Tests**: `Test[StructName]_[MethodName]`
- **Method Tests with Options**: `Test[StructName]_[MethodName]_[Option]`

Examples:
```go
func TestNewFrame(t *testing.T) { /* Basic test */ }
func TestGroupSequence_String(t *testing.T) { /* Method test */ }
func TestParameters_String_NilValue(t *testing.T) { /* Method test with option */ }
```

### 2. Test Case Structure
**Mandatory**: Test cases must use `map[string]struct{...}` format with test case names as keys for table-driven tests **only when there are multiple cases**.

For single test cases, write the test code directly without using a map.

```go
// Single test case example
func TestExample_SingleCase(t *testing.T) {
    input := "test"
    expected := "test_result"

    result, err := SomeFunction(input)

    assert.NoError(t, err)
    assert.Equal(t, expected, result)
}

// Multiple test cases example
func TestExample_MultipleCases(t *testing.T) {
    tests := map[string]struct {
        input    string
        expected string
        wantErr  bool
    }{
        "valid input": {
            input:    "test",
            expected: "test_result",
            wantErr:  false,
        },
        "empty input": {
            input:    "",
            expected: "",
            wantErr:  true,
        },
    }

    for name, tt := range tests {
        t.Run(name, func(t *testing.T) {
            result, err := SomeFunction(tt.input)

            if tt.wantErr {
                assert.Error(t, err)
            } else {
                assert.NoError(t, err)
                assert.Equal(t, tt.expected, result)
            }
        })
    }
}
```

### 3. Required Packages and Assertions
- **Use testify package**: All tests must use `github.com/stretchr/testify`
- **assert for comparisons**: `assert.Equal(t, expected, actual)`, `assert.NoError(t, err)`, etc.
- **require for preconditions**: `require.NoError(t, err)`, `require.NotNil(t, obj)`, etc.
- **PROHIBITED**: `testify/mock` (`mock.Mock`) is not used in this project

```go
import (
    "testing"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)
```

### 4. Fake/Mock Definitions and Management
**Mandatory**: Test doubles must be defined in the same package (`package [packagename]`) within dedicated `fake_XXX_test.go` or `mock_XXX_test.go` files.

#### Policy: Func-field pattern only
- All test doubles use the **Func-field pattern**. `testify/mock` (`mock.Mock`) is **not used**.
- Prefer state/result assertions over call-order assertions.

#### Naming Convention
- **`Fake*` prefix**: Test doubles that model real system behavior with realistic defaults (e.g., `FakeQUICStream` models quic-go stream semantics — idempotent Close, CancelWrite, CancelRead via `context.WithCancelCause`).
- **`Mock*` prefix**: Simple Func-field stubs that provide zero-value defaults without modeling specific system behavior (e.g., `MockStreamConn`).

#### File Organization
- **Existing Fakes/Mocks**: Check for existing files before creating new ones
- **File Naming**: `fake_[feature]_test.go` or `mock_[feature]_test.go` (e.g., `fake_quic_stream_test.go`, `mock_quic_connection_test.go`)
- **Package Declaration**: Always use `package [packagename]` (not `package [packagename]_test`)

#### Test Double Structure Pattern
All test doubles use the **Func-field pattern**:

```go
// In fake_example_test.go or mock_example_test.go
package moqt

var _ SomeInterface = (*MockExample)(nil)

type MockExample struct {
    SomeMethodFunc func(arg string) error
}

func (m *MockExample) SomeMethod(arg string) error {
    if m.SomeMethodFunc != nil {
        return m.SomeMethodFunc(arg)
    }
    return nil // sensible zero-value default
}
```

#### Default-Oriented Design
Func-field methods must provide **sensible defaults** when the field is nil, so tests only configure what they actually need:

- **Write-like methods**: Default to success (`return len(p), nil`)
- **Read-like methods**: Default to EOF (`return 0, io.EOF`)
- **Close/Cancel methods**: Default to no-op or idempotent behavior
- **Context methods**: Default to `context.Background()`
- **Address methods**: Default to `&net.TCPAddr{}`

This eliminates boilerplate: tests that don't care about a method's behavior use `&FakeXxx{}` with zero configuration.

#### ParentCtx Pattern
Fakes that expose `Context()` accept an optional `ParentCtx` field to derive cancellation from:

```go
type FakeQUICStream struct {
    ParentCtx context.Context // optional; defaults to context.Background()
    // ... Func fields ...
}
```

#### Implementation Rules
1. **Func-field only**: All test doubles use Func-field pattern. `testify/mock` is prohibited.
2. **Nil check required**: All methods must check if the Func field is nil before calling it and return a sensible default.
3. **Model real behavior** (for `Fake*`): Fakes should model the real system's behavior (e.g., idempotent Close, EOF on Read, CancelCause propagation).
4. **Interface compliance**: Always add a compile-time interface check: `var _ SomeInterface = (*FakeExample)(nil)`

#### Discovery Rules
1. **Search First**: Before creating new test doubles, search for existing fake/mock files in the same package
2. **Reuse Existing**: If a suitable test double already exists, use it instead of creating a duplicate
3. **Create New**: Only create new files when no suitable test double exists
4. **Consistent Naming**: Follow the existing pattern: `Fake[Name]` for behavior-modeling doubles, `Mock[Name]` for simple stubs
5. **No Factory Functions for test doubles**: Do not create `createMock...()`, `newMock...()`, or similar helper functions for mock initialization
6. **Shared setup helpers are allowed**: Helpers like `newTestConn()` that configure a test double with common defaults are allowed when repeated 3+ times

### 5. Package Declaration
- **Internal Tests**: `package [packagename]` - Can test private functions and access internal structures
- **External Tests**: `package [packagename]_test` - Test only public APIs
- **Mock Files**: Always use `package [packagename]` - Mocks must be in the same package as the interfaces they implement

### 6. Interface Testing Policy
**PROHIBITED**: Do not create test files or test functions for interface definitions themselves.

#### Rules for Interface Files
1. **No Test File Generation**: Never create `[interface_name]_test.go` files for files that only contain interface definitions
2. **No Interface Test Functions**: Do not write test functions that test interface behavior directly
3. **Interface Testing Through Implementations**: Test interfaces indirectly through their concrete implementations
4. **Mock Creation Only**: For interface files, only create corresponding mock files in `mock_[interface_name]_test.go`

#### Examples of What NOT to Do
```go
// ❌ PROHIBITED: Do not create test files for interface-only files
// File: writer_interface_test.go
func TestWriterInterface(t *testing.T) {
    // This should never exist
}

// ❌ PROHIBITED: Do not test interface methods directly
func TestAnnouncementWriter_SendAnnouncements(t *testing.T) {
    // Interface methods should not be tested directly
}
```

#### Correct Approach
```go
// ✅ CORRECT: Test concrete implementations that implement the interface
func TestConcreteWriter_SendAnnouncements(t *testing.T) {
    writer := NewConcreteWriter()
    // Test the concrete implementation
}

// ✅ CORRECT: Create mocks for interfaces in mock files
// File: mock_announcement_writer_test.go
type MockAnnouncementWriter struct {
    mock.Mock
}
```

## Test File Structure

### File Naming
- `[filename]_test.go` (regular test files)
- `mock_[feature]_test.go` (dedicated mock files, always in same package)
- **PROHIBITED**: Do not create test files for interface-only source files

### Test Categories

#### 1. Unit Tests
Test basic functionality of each feature:
```go
func TestNewObject(t *testing.T) {
    obj := NewObject("param")
    assert.NotNil(t, obj)
}
```

#### 2. Method Tests
Test struct methods with comprehensive cases:
```go
func TestObject_Method(t *testing.T) {
    tests := map[string]struct {
        setup    func() *Object
        input    string
        expected string
        wantErr  bool
    }{
        "success case": {
            setup: func() *Object { return NewObject("test") },
            input: "input",
            expected: "expected",
            wantErr: false,
        },
        "error case": {
            setup: func() *Object { return NewObject("") },
            input: "invalid",
            expected: "",
            wantErr: true,
        },
    }

    for name, tt := range tests {
        t.Run(name, func(t *testing.T) {
            obj := tt.setup()
            result, err := obj.Method(tt.input)
            
            if tt.wantErr {
                assert.Error(t, err)
            } else {
                assert.NoError(t, err)
                assert.Equal(t, tt.expected, result)
            }
        })
    }
}
```

#### 3. Error Handling Tests
Explicitly test error cases:
```go
func TestObject_Method_Error(t *testing.T) {
    tests := map[string]struct {
        setup       func() *Object
        input       string
        expectError bool
        errorType   error
    }{
        "invalid input": {
            setup:       func() *Object { return NewObject("") },
            input:       "",
            expectError: true,
            errorType:   ErrInvalidInput,
        },
    }

    for name, tt := range tests {
        t.Run(name, func(t *testing.T) {
            obj := tt.setup()
            _, err := obj.Method(tt.input)
            
            if tt.expectError {
                assert.Error(t, err)
                if tt.errorType != nil {
                    assert.ErrorIs(t, err, tt.errorType)
                }
            } else {
                assert.NoError(t, err)
            }
        })
    }
}
```

#### 4. Boundary Value Tests
Test edge cases, minimum, maximum, zero values:
```go
func TestObject_BoundaryValues(t *testing.T) {
    tests := map[string]struct {
        value    int
        expected bool
    }{
        "zero value":     {value: 0, expected: false},
        "minimum value":  {value: 1, expected: true},
        "maximum value":  {value: math.MaxInt, expected: true},
        "negative value": {value: -1, expected: false},
    }

    for name, tt := range tests {
        t.Run(name, func(t *testing.T) {
            result := ValidateValue(tt.value)
            assert.Equal(t, tt.expected, result)
        })
    }
}
```

#### 5. Helper Functions
Define test helper functions in the same file or dedicated `*_test.go` files:

Helper implementation policy:
- **Reuse existing helpers first**: Before adding a new helper, check whether an existing helper can express the setup.
- **Use helpers for repeated non-mock setup**: If the same setup appears 3+ times (e.g., request fixtures, common context/config), extract/use a helper.
- **Do not over-abstract**: If only one test needs custom setup, keep it inline for readability.
- **Default-oriented helpers are preferred**: Provide sensible defaults and expose only parameters that actually vary in many tests.
- **Helper signature**: Prefer `testing.TB` and call `tb.Helper()` inside helpers.
- **Test double setup helpers are allowed**: Shared helpers like `newTestConn()` are allowed when the same test double configuration appears 3+ times. They must not hide test-critical behavior.

Pattern example (recommended):

```go
func testSubscribeRequest(tb testing.TB, config *SubscribeConfig) *SubscribeRequest {
    tb.Helper()
    req, err := NewSubscribeRequest("/test", "video", config)
    require.NoError(tb, err)
    return req
}
```

```go
// createTestContext creates a test context for testing purposes
func createTestContext() *Context {
    return &Context{
        // Test configuration
    }
}

// Helper function to verify heap property
func isValidHeap(h *Heap) bool {
    // Helper logic
    return true
}
```

#### 6. Concurrent Testing
Use proper synchronization for concurrent tests. Prefer `testing/synctest` for tests that depend on goroutine scheduling, timers (time.Sleep, time.Timer, time.Ticker), or deterministic ordering of concurrent operations. `synctest` lets you run a test in an isolated "bubble" where time is deterministic and goroutine lifetimes are enforced.

The basic rule: If your test spawns goroutines that must be observed deterministically, rely on `synctest.Test` + `synctest.Wait` instead of ad-hoc sleeps or global waits.

```go
import (
    "testing"
    "testing/synctest"
    "sync"
)

func TestConcurrentAccess(t *testing.T) {
    // Simple sync.WaitGroup case — local WaitGroup associated with the bubble
    synctest.Test(t, func(t *testing.T) {
        var wg sync.WaitGroup
        wg.Add(1)
        go func() {
            defer wg.Done()
            // concurrent work
        }()
        // Wait deterministically for all goroutines to block or finish
        wg.Wait()
    })
}
```

Notes and rules for `testing/synctest`:
- Use `synctest.Test(t, func(t *testing.T) { ... })` to run tests in an isolated bubble.
- Use `synctest.Wait()` to wait until all goroutines in the bubble (other than the caller) are durably blocked. This is preferred to `time.Sleep` in tests that rely on scheduling or timers.
- `Wait` must not be called outside a synctest bubble and must not be called concurrently by multiple goroutines in the same bubble.
- Do not call `t.Run`, `t.Parallel`, or `t.Deadline` from within the synctest bubble; they are not supported.
- `T.Cleanup` functions registered within the bubble run inside the bubble and execute immediately before the bubble exits.
- `T.Context()` returns a `context.Context` with a `Done` channel associated with the bubble; timeouts and cancellations created from that context are scoped to the bubble.
- Local `sync.WaitGroup`s (e.g., `var wg sync.WaitGroup`) become associated with the current bubble when `Add` or `Go` is called from that bubble. Do not rely on package-level `var wg sync.WaitGroup` variables — they cannot be associated with a bubble and may not be durably blocking.
- Some operations are not considered durably blocking and therefore will not cause `synctest.Wait()` to return, such as `sync.Mutex` lock waits, network I/O, or system calls. Prefer in-process fakes (e.g., `net.Pipe`) or mocks when testing network behavior.
- Operations that are durably blocking include blocking `chan` send/receive (for channels created within the bubble), `sync.Cond.Wait`, `sync.WaitGroup.Wait` (when `Add`/`Go` was called in the bubble), and `time.Sleep`.
- Timers and time-dependent code: In a synctest bubble, time advances only when all goroutines are durably blocked; this makes testing time-related behavior deterministic.

Examples:

```go
// Deterministic time example: time in the bubble starts at midnight UTC 2000-01-01
func TestTimeAdvance(t *testing.T) {
    // imports for this snippet: testing, testing/synctest, time
    synctest.Test(t, func(t *testing.T) {
        start := time.Now()
        go func() {
            time.Sleep(1 * time.Second)
            t.Log(time.Since(start)) // always logs "1s"
        }()
        // This later sleep returns after the goroutine above progressed
        time.Sleep(2 * time.Second)
        t.Log(time.Since(start))    // always logs "2s"
    })
}

// Using Wait to observe completion/state
func TestWait(t *testing.T) {
    // imports for this snippet: testing, testing/synctest, github.com/stretchr/testify/assert
    synctest.Test(t, func(t *testing.T) {
        done := false
        go func() {
            done = true
        }()
        synctest.Wait()
        assert.True(t, done)
    })
}
```

When to prefer synctest:
- Tests that rely on timers, `time.Sleep`, `time.Timer`, or `time.Ticker`.
- Tests that start background goroutines where you want deterministic lifecycle management.
- Tests that assert interactions that depend on a specific order or blocking behavior.

When not to use synctest:
- Tests that require real network I/O or external processes; prefer mocking or in-process fakes.
- Tests that must use `t.Parallel` or nested `t.Run` with subtests using parallelism.

Other concurrent testing best practices:
- Keep goroutines deterministic where possible by using `synctest` or explicit synchronization. Avoid arbitrary `time.Sleep` calls.
- Ensure tests clean up all goroutines by the end of the test; `synctest.Test` will panic on deadlock.
- Document any non-obvious synchronization in test code comments to avoid flaky tests.

#### 7. Context and Timeout Tests
Tests involving context or timeouts:
```go
func TestWithTimeout(t *testing.T) {
    ctx, cancel := context.WithTimeout(context.Background(), time.Second)
    defer cancel()
    // Test implementation
}
```

## Assertion Patterns

### Basic Comparisons
```go
assert.Equal(t, expected, actual)
assert.NotEqual(t, notExpected, actual)
assert.True(t, condition)
assert.False(t, condition)
assert.Nil(t, object)
assert.NotNil(t, object)
```

### Error Handling
```go
assert.NoError(t, err)
assert.Error(t, err)
assert.ErrorIs(t, err, expectedErr)
assert.ErrorAs(t, err, &target)
```

### Collections
```go
assert.Len(t, collection, expectedLength)
assert.Empty(t, collection)
assert.Contains(t, collection, element)
assert.ElementsMatch(t, expected, actual)
```

## Best Practices

1. **Independent Test Cases**: Do not share state between test cases
2. **Clear Test Names**: Test case names should clearly indicate what is being tested
3. **Boundary Value Testing**: Test zero values, maximum values, minimum values, nil values
4. **Error Cases**: Test both normal and error cases
5. **Mock Discovery**: Always search for existing mocks before creating new ones
6. **Mock Package Placement**: All mocks must be in the same package (`package [packagename]`), never in `_test` packages
7. **Mock File Organization**: Use dedicated `fake_[feature]_test.go` or `mock_[feature]_test.go` files for test double definitions
8. **Func-field pattern only**: All test doubles use Func-field pattern; `testify/mock` is prohibited
9. **Default-oriented design**: Func-field methods provide sensible defaults when nil, so tests only configure what they need
10. **No test double factory functions**: Initialize and configure test doubles directly in each test; shared setup helpers (e.g., `newTestConn()`) are allowed for non-mock repeated setup (3+ times)
10. **Interface Testing Policy**: **PROHIBITED** - Never create test files or test functions for interface definitions themselves
11. **Setup and Cleanup**: Use setup/teardown functions when necessary
12. **Comments**: Add comments for complex test logic

## Test Double Patterns and Management

### File Discovery
Before creating any test double, always search for existing implementations:
1. Look for `fake_[feature]_test.go` or `mock_[feature]_test.go` files in the same package
2. Check if the required interface fake/mock already exists
3. Only create new files when no suitable test double exists

### File Creation
When creating new test double files:
```go
// File: fake_example_test.go (or mock_example_test.go)
package moqt  // Always same package, never _test

var _ ExampleInterface = (*FakeExample)(nil)

type FakeExample struct {
    ParentCtx      context.Context     // optional; defaults to context.Background()
    SomeMethodFunc func(arg string) error
}

func (f *FakeExample) SomeMethod(arg string) error {
    if f.SomeMethodFunc != nil {
        return f.SomeMethodFunc(arg)
    }
    return nil // sensible default
}
```

### Initialization and Usage
Test doubles are initialized directly in each test function. Configure only the Func fields needed for the test.

#### Usage Examples
```go
// Minimal: rely on sensible defaults (Read→EOF, Write→success)
func TestWithDefaults(t *testing.T) {
    stream := &FakeQUICStream{}  // no setup needed
    _, err := stream.Read(make([]byte, 10))
    assert.ErrorIs(t, err, io.EOF)
}

// Configure only what the test needs
func TestWithCustomBehavior(t *testing.T) {
    stream := &FakeQUICStream{
        ReadFunc: func(p []byte) (int, error) {
            copy(p, []byte("hello"))
            return 5, nil
        },
    }
    buf := make([]byte, 10)
    n, err := stream.Read(buf)
    assert.NoError(t, err)
    assert.Equal(t, 5, n)
}

// Shared setup helper for repeated configuration (3+ uses)
func newTestConn() *MockStreamConn {
    conn := &MockStreamConn{}
    conn.AcceptStreamFunc = func(context.Context) (transport.Stream, error) { return nil, io.EOF }
    conn.AcceptUniStreamFunc = func(context.Context) (transport.ReceiveStream, error) { return nil, io.EOF }
    conn.RemoteAddrFunc = func() net.Addr { return &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8080} }
    return conn
}

// Table-driven test with Func-field fakes
func TestTableDrivenWithFakes(t *testing.T) {
    tests := map[string]struct {
        setupStream func() *FakeQUICStream
        wantErr     bool
    }{
        "default stream": {
            // nil setupStream → use &FakeQUICStream{} with defaults
        },
        "custom read": {
            setupStream: func() *FakeQUICStream {
                return &FakeQUICStream{
                    ReadFunc: func(p []byte) (int, error) {
                        return 0, errors.New("read error")
                    },
                }
            },
            wantErr: true,
        },
    }

    for name, tt := range tests {
        t.Run(name, func(t *testing.T) {
            var stream *FakeQUICStream
            if tt.setupStream != nil {
                stream = tt.setupStream()
            } else {
                stream = &FakeQUICStream{}
            }
            // ... test logic ...
            _ = stream
        })
    }
}

// Variadic option pattern for helpers with optional configuration
func newTestWriter(t *testing.T, opts ...func(*FakeQUICStream)) (*Writer, *FakeQUICStream) {
    t.Helper()
    stream := &FakeQUICStream{}
    for _, opt := range opts {
        opt(stream)
    }
    return NewWriter(stream), stream
}
```

#### Prohibited Patterns
```go
// ❌ DO NOT use testify/mock
type MockExample struct {
    mock.Mock  // PROHIBITED
}

// ❌ DO NOT create factory functions for test doubles
func createMockExample() *MockExample {
    return &MockExample{}
}

// ❌ DO NOT pass empty callbacks when the default behavior suffices
stream := &FakeQUICStream{
    ReadFunc: func(p []byte) (int, error) {
        return 0, io.EOF  // redundant — this is already the default
    },
}
// ✅ Instead, use:
stream := &FakeQUICStream{}
```

### Usage Guidelines
1. **Func-field only**: All test doubles use Func-field pattern. `testify/mock` is prohibited.
2. **Nil check + default**: All methods must check if the Func field is nil and return a sensible default when nil.
3. **Direct Initialization**: Initialize test doubles directly in test functions using `&FakeXxx{}` or `&MockXxx{}`.
4. **Minimal configuration**: Only set Func fields that the test actually exercises. Rely on defaults for everything else.
5. **Shared setup helpers**: Allowed for repeated non-mock configuration (3+ times). Example: `newTestConn()` for common connection setup.
6. **Variadic option pattern**: Use `...func(*FakeXxx)` for test helpers where optional configuration is sometimes needed, to avoid passing empty callbacks.
7. **Behavior modeling** (`Fake*`): Fakes should model the real system's semantics (e.g., idempotent Close, CancelCause propagation, EOF on Read).
8. **ParentCtx pattern**: Use a `ParentCtx` field for test doubles that need to derive context cancellation from a parent.

Follow these rules to generate consistent test code.