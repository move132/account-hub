package logging

import (
	"crypto/rand"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	MaxEntries     = 1000
	maxBufferBytes = 4 << 20
	maxRecordBytes = 8 << 10
)

// Entry contains only the sanitized, bounded representation sent to the UI.
type Entry struct {
	ID      string `json:"id"`
	Time    int64  `json:"time"`
	Level   string `json:"level"`
	Message string `json:"message"`
	Fields  string `json:"fields"`
}

type bufferedEntry struct {
	Entry
	sequence uint64
	size     int
}

type Snapshot struct {
	Items     []Entry         `json:"items"`
	Reset     bool            `json:"reset"`
	Truncated bool            `json:"truncated"`
	Cursor    string          `json:"-"`
	Changed   <-chan struct{} `json:"-"`
	Closed    bool            `json:"-"`
}

// Buffer uses one notification channel for all readers. A slow reader never
// holds up a writer; it resumes from the retained history and detects any gap.
type Buffer struct {
	mu       sync.Mutex
	instance string
	entries  []bufferedEntry
	start    int
	count    int
	bytes    int
	maxBytes int
	sequence uint64
	changed  chan struct{}
	closed   bool
}

func NewBuffer() *Buffer {
	return &Buffer{
		instance: rand.Text(), entries: make([]bufferedEntry, MaxEntries),
		maxBytes: maxBufferBytes, changed: make(chan struct{}),
	}
}

func (b *Buffer) append(entry Entry) {
	entry.Message = limitText(entry.Message, maxRecordBytes/2)
	entry.Fields = limitText(entry.Fields, maxRecordBytes-len(entry.Message))
	if entry.Time == 0 {
		entry.Time = time.Now().UnixMilli()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.sequence++
	entry.ID = b.cursor(b.sequence)
	size := len(entry.ID) + len(entry.Level) + len(entry.Message) + len(entry.Fields) + 64
	for b.count > 0 && (b.count == len(b.entries) || b.bytes+size > b.maxBytes) {
		b.bytes -= b.entries[b.start].size
		b.entries[b.start] = bufferedEntry{}
		b.start = (b.start + 1) % len(b.entries)
		b.count--
	}
	b.entries[(b.start+b.count)%len(b.entries)] = bufferedEntry{Entry: entry, sequence: b.sequence, size: size}
	b.count++
	b.bytes += size
	close(b.changed)
	b.changed = make(chan struct{})
}

// Snapshot atomically reads history and obtains the next notification, avoiding
// a gap between replay and live delivery. Cursors include a process identifier.
func (b *Buffer) Snapshot(after string) Snapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	result := Snapshot{
		Cursor:  b.cursor(b.sequence),
		Changed: b.changed, Closed: b.closed,
	}
	var sequence uint64
	if after != "" {
		instance, value, ok := strings.Cut(after, ":")
		parsed, err := strconv.ParseUint(value, 10, 64)
		if !ok || err != nil || instance != b.instance || parsed > b.sequence {
			result.Reset = true
		} else {
			sequence = parsed
		}
	}
	offset := 0
	if b.count > 0 {
		oldest := b.entries[b.start].sequence
		result.Truncated = oldest > 1 && sequence < oldest-1
		if sequence >= oldest {
			offset = int(min(sequence-oldest+1, uint64(b.count)))
		}
	}
	result.Items = make([]Entry, 0, b.count-offset)
	for i := offset; i < b.count; i++ {
		entry := b.entries[(b.start+i)%len(b.entries)]
		result.Items = append(result.Items, entry.Entry)
	}
	return result
}

func (b *Buffer) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.closed {
		b.closed = true
		close(b.changed)
	}
}

func (b *Buffer) cursor(sequence uint64) string {
	return b.instance + ":" + strconv.FormatUint(sequence, 10)
}
