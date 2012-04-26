// Copyright 2012 Alec Thomas
// 
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
// 
//   http://www.apache.org/licenses/LICENSE-2.0
// 
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package vheap

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

const (
	regionHeaderSize        = 32
	regionSignature         = 0
	regionFreePointerOffset = 8
	regionSizeOffset        = 16
	regionId                = 24

	// Block list offsets from top of region
	blockListHeaderSize = 8 // Size of header
	blockListNextIdPtr  = 0

	// Size of each entry in the block list
	blockListEntrySize = 16 // Size of each entry in the block list (offset, size int64)
)

var (
	InvalidSignature = errors.New("Invalid region signature")
	signature        = []byte("HEAPREGN")
)

type region struct {
	id                 int64
	f                  *os.File
	d                  []byte
	freePtr            []byte
	blockListNextIdPtr []byte
}

type BlockId int64

type Block struct {
	region *region
	Id     BlockId
	Bytes  []byte
	Size   int64
	Offset int64
}

// BlockId
func NewBlockId(rid, bid int64) BlockId {
	if bid > 0xfffff {
		panic("Block ID cannot be > 65535")
	}
	return BlockId((rid << 16) | (bid & 0xffff))
}

func (id BlockId) RegionId() int64 {
	return int64(id) >> 16
}

func (id BlockId) BlockId() int64 {
	return int64(id) & 0xffff
}

func (id BlockId) String() string {
	return fmt.Sprintf("BlockId{Region: %d, Block: %d}", id.RegionId(), id.BlockId())
}

// Block
func (b *Block) String() string {
	return fmt.Sprintf("Block{Id: %v, Size: %d, Offset: %d}", b.Id, b.Size, b.Offset)
}

// Size of region, including bookkeeping overhead.
func (r *region) Size() int64 {
	return int64(len(r.d))
}

func (r *region) Available() int64 {
	return r.Size() - r.getFreePointer() - blockListHeaderSize - r.getNextFreeBlockId().BlockId()*blockListEntrySize
}

func (r *region) Allocate(size int64) (*Block, error) {
	offset := r.getFreePointer()
	if size > r.Available() {
		return nil, OutOfMemory
	}
	if size > r.Size() {
		return nil, BlockTooLarge
	}
	id := r.getNextFreeBlockId()
	r.setBlockListEntry(id, offset, size)
	b := r.rawGetBlock(id)
	// We've allocated the block, now we update the free pointer and increment
	// the ID counter. Ideally this would be atomic, but there you go.
	r.setFreePointer(offset + size)
	r.incrementFreeBlockId()
	return b, nil
}

func (r *region) Free(b *Block) bool {
	d := r.getBlockListEntryBytes(b.Id)
	offset, size := *(*int64)(unsafe.Pointer(&d[0])), *(*int64)(unsafe.Pointer(&d[8]))
	if offset == 0 && size == 0 {
		return false
	}
	r.setBlockListEntry(b.Id, 0, 0)
	// If we're the last block, wind back the heap.
	if b.Id == r.getNextFreeBlockId()-1 {
		r.setFreePointer(offset)
		r.setNextFreeBlockId(b.Id)
	}
	return true
}

// Return array of allocated ids in this region
func (r *region) Blocks() []*Block {
	max := r.getNextFreeBlockId()
	entries := make([]*Block, 0, max)
	for i := NewBlockId(r.id, 0); i < max; i++ {
		if b := r.GetBlock(i); b != nil {
			entries = append(entries, b)
		}
	}
	return entries
}

func (r *region) GetBlock(id BlockId) *Block {
	max := r.getNextFreeBlockId()
	if id < NewBlockId(r.id, 0) || id >= max {
		return nil
	}
	return r.rawGetBlock(id)
}

// Internal functions
func (r *region) rawGetBlock(id BlockId) *Block {
	d := r.getBlockListEntryBytes(id)
	offset, size := *(*int64)(unsafe.Pointer(&d[0])), *(*int64)(unsafe.Pointer(&d[8]))
	if offset == 0 || size == 0 {
		return nil
	}
	return &Block{
		region: r,
		Id:     id,
		Bytes:  r.d[offset : offset+size],
		Size:   size,
		Offset: offset,
	}
}

func openRegion(f *os.File, writeable bool, offset int64) (*region, error) {
	_, err := f.Seek(offset, os.SEEK_SET)
	if err != nil {
		return nil, err
	}
	header := make([]byte, regionHeaderSize)
	_, err = f.Read(header)
	if err != nil {
		return nil, err
	}
	if bytes.Compare(header[:8], signature) != 0 {
		return nil, InvalidSignature
	}
	size := *(*int64)(unsafe.Pointer(&header[regionSizeOffset]))
	rid := *(*int64)(unsafe.Pointer(&header[regionId]))
	flags := syscall.PROT_READ
	if writeable {
		flags |= syscall.PROT_WRITE
	}
	d, err := syscall.Mmap(int(f.Fd()), offset, int(size), flags, syscall.MAP_SHARED)
	if err != nil {
		return nil, err
	}
	r := &region{
		id:                 rid,
		f:                  f,
		d:                  d,
		freePtr:            d[regionFreePointerOffset : regionFreePointerOffset+8],
		blockListNextIdPtr: d[len(d)-8 : len(d)],
	}
	return r, nil
}

func appendRegion(rid int64, f *os.File, regionSizeB int64) (*region, error) {
	size, err := f.Seek(0, os.SEEK_END)
	if err != nil {
		return nil, err
	}
	if err := f.Truncate(size + regionSizeB); err != nil {
		return nil, err
	}

	// Initialize header
	header := make([]byte, regionHeaderSize)
	copy(header[:8], signature)
	*(*int64)(unsafe.Pointer(&header[regionFreePointerOffset])) = regionHeaderSize
	*(*int64)(unsafe.Pointer(&header[regionSizeOffset])) = regionSizeB
	*(*int64)(unsafe.Pointer(&header[regionId])) = rid
	if _, err := f.Write(header); err != nil {
		return nil, err
	}
	r, err := openRegion(f, true, size)
	if err != nil {
		return nil, err
	}
	r.initBlockList()
	return r, nil
}

func (r *region) getNextFreeBlockId() BlockId {
	return NewBlockId(r.id, *(*int64)(unsafe.Pointer(&r.blockListNextIdPtr[0])))
}

func (r *region) setNextFreeBlockId(id BlockId) {
	*(*int64)(unsafe.Pointer(&r.blockListNextIdPtr[0])) = id.BlockId()
}

func (r *region) incrementFreeBlockId() BlockId {
	id := r.getNextFreeBlockId()
	*(*int64)(unsafe.Pointer(&r.blockListNextIdPtr[0])) = id.BlockId() + 1
	return id
}

func (r *region) getFreePointer() int64 {
	return *(*int64)(unsafe.Pointer(&r.freePtr[0]))
}

func (r *region) setFreePointer(offset int64) {
	*(*int64)(unsafe.Pointer(&r.freePtr[0])) = offset
}

func (r *region) setBlockListEntry(id BlockId, offset, size int64) {
	d := r.getBlockListEntryBytes(id)
	*(*int64)(unsafe.Pointer(&d[0])) = offset
	*(*int64)(unsafe.Pointer(&d[8])) = size
}

func (r *region) getBlockListEntryBytes(id BlockId) []byte {
	i := int64(len(r.d)) - blockListHeaderSize - blockListEntrySize*id.BlockId()
	j := i - blockListEntrySize
	return r.d[j:i]
}

func (r *region) initBlockList() {
	*(*int64)(unsafe.Pointer(&r.blockListNextIdPtr[0])) = 0
}
