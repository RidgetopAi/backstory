package socket

import "time"

// FirstLineDeadline bounds how long handle waits, per accepted connection,
// for the connecting peer to send its complete first request line. A peer
// that connects and never sends '\n' would otherwise pin its handling
// goroutine forever (critic T1 on task 73333c8c). The deadline is applied
// fresh to every new connection, not shared across the daemon's lifetime.
const FirstLineDeadline = 5 * time.Second

// MaxLineLength bounds the first request line's size in bytes. A peer that
// sends more than this many bytes without a '\n' is disconnected instead of
// letting the read buffer grow without bound (critic T1 on task 73333c8c).
const MaxLineLength = 1 << 20 // 1 MiB
