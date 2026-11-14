package log

import (
	"io"
	"os"
)

const (
	offsetSize   uint64 = 4
	positionSize uint64 = 8
	entrySize           = offsetSize + positionSize
)

type index struct {
	file         *os.File
	writtenBytes uint64
	maxBytes     uint64
}

func newIndex(indexFile *os.File, config Config) (*index, error) {
	fileInfo, err := os.Stat(indexFile.Name())
	if err != nil {
		return nil, err
	}
	return &index{
		file:         indexFile,
		writtenBytes: uint64(fileInfo.Size()),
		maxBytes:     config.Segment.MaxIndexBytes,
	}, nil
}

func (idx *index) Close() error {
	if err := idx.file.Sync(); err != nil {
		return err
	}
	return idx.file.Close()
}

func (idx *index) Read(requestedOffset int64) (recordOffset uint32, storePosition uint64, err error) {
	if idx.writtenBytes == 0 {
		return 0, 0, io.EOF
	}

	if requestedOffset == -1 {
		recordOffset = uint32((idx.writtenBytes / entrySize) - 1)
	} else {
		recordOffset = uint32(requestedOffset)
	}

	entryStart := uint64(recordOffset) * entrySize
	if idx.writtenBytes < entryStart+entrySize {
		return 0, 0, io.EOF
	}

	var buf [entrySize]byte
	if _, err := idx.file.ReadAt(buf[:], int64(entryStart)); err != nil {
		return 0, 0, err
	}
	recordOffset = encoding.Uint32(buf[0:offsetSize])
	storePosition = encoding.Uint64(buf[offsetSize:entrySize])
	return recordOffset, storePosition, nil
}

func (idx *index) Write(recordOffset uint32, storePosition uint64) error {
	if idx.maxBytes < idx.writtenBytes+entrySize {
		return io.EOF
	}
	var buf [entrySize]byte
	encoding.PutUint32(buf[0:offsetSize], recordOffset)
	encoding.PutUint64(buf[offsetSize:entrySize], storePosition)

	if _, err := idx.file.WriteAt(buf[:], int64(idx.writtenBytes)); err != nil {
		return err
	}
	idx.writtenBytes += entrySize
	return nil
}

func (idx *index) Name() string {
	return idx.file.Name()
}
