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

// See README for index format.

package vheap

import (
	"errors"
	"os"
)

var (
	OutOfMemory   = errors.New("Out of memory")
	BlockTooLarge = errors.New("Block size exceeds region size")
)

type Heap struct {
	f       *os.File
	regions []*region
}

func OpenForUpdate(filename string, regionSizeMb int64) (*Heap, error) {
	f, err := os.OpenFile(filename, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	i, err := f.Stat()
	if err != nil {
		return nil, err
	}
	// Newly created, append region.
	var h *Heap
	if i.Size() == 0 {
		r, err := appendRegion(0, f, regionSizeMb*1024*1024)
		if err != nil {
			f.Close()
			return nil, err
		}
		h = &Heap{f, []*region{r}}
	} else {
		h, err = initHeap(f, true)
		if err != nil {
			f.Close()
			return nil, err
		}
	}
	return h, nil
}

func Open(filename string) (*Heap, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	return initHeap(f, false)
}

func (h *Heap) Close() {
	h.f.Close()
	*h = Heap{}
}

func (h *Heap) Available() int64 {
	c := int64(0)
	for _, r := range h.regions {
		c += r.Available()
	}
	return c
}

func (h *Heap) GetBlock(id BlockId) *Block {
	r := h.regions[id.RegionId()]
	return r.GetBlock(id)
}

// Return number of blocks allocated.
func (h *Heap) Blocks() []*Block {
	blocks := make([]*Block, 0, 128)
	for _, r := range h.regions {
		blocks = append(blocks, r.Blocks()...)
	}
	return blocks
}

func (h *Heap) Allocate(size int64) (*Block, error) {
	var r *region
	for _, r = range h.regions {
		b, err := r.Allocate(size)
		if err == nil {
			return b, nil
		}
		if err != OutOfMemory {
			return nil, err
		}
	}
	// Ensure the new region has enough capacity to fit the new block.
	regionSize := r.Size()
	for regionSize < size {
		regionSize *= 2
	}
	// If we've hit here we need to add another region...
	r, err := appendRegion(r.id+1, h.f, regionSize)
	if err != nil {
		return nil, err
	}
	h.regions = append(h.regions, r)
	return h.Allocate(size)
}

func (h *Heap) Free(b *Block) bool {
	r := h.regions[b.Id.RegionId()]
	return r.Free(b)
}

// Internal methods
func initHeap(f *os.File, writeable bool) (*Heap, error) {
	regions := make([]*region, 0, 16)
	offset := int64(0)
	i, err := f.Stat()
	if err != nil {
		return nil, err
	}
	for offset < i.Size() {
		region, err := openRegion(f, writeable, offset)
		if err != nil {
			return nil, err
		}
		regions = append(regions, region)
		offset += int64(len(region.d))
	}
	h := &Heap{
		f:       f,
		regions: regions,
	}
	return h, nil
}
