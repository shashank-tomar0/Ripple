// Time helpers shared across the Ripple app.
//
// The Ripple wire format stamps timestamps in Unix **nanoseconds**, matching
// the Go daemon (message.Timestamp = time.Now().UnixNano()). Every Dart side
// that creates a wire timestamp MUST go through unixNanosNow() so the two
// halves never drift apart (a previous bug had Go in nanos and Dart in micros).
library;

/// Current time in Unix nanoseconds — the Ripple wire timestamp format.
int unixNanosNow() => DateTime.now().microsecondsSinceEpoch * 1000;
