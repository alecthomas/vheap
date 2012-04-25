# Virtual Heap

This [Go](http://golang.org) package provides fast, mmapped access to a virtual heap. Each block allocated from the heap is assigned a unique, persistent reference ID that can be used to address the block across application runs.

The implementation is currently very basic, but sufficient for its intended purpose.

## Use-cases

Why would you want to use this?

It is useful for situations where you want very low overhead object creation and tight control over the layout of and access to, data. Its initial use will be as a search index, storing posting lists, document names, scoring information, and so on.

## Example
Creating a heap and adding some data:

    // Create a heap with an initial size of 32MB.
    h, err := vheap.OpenForUpdate("hello.vheap", 32)
    defer h.Close()
    
    // Allocate a 32 byte block.
    b, err := h.Allocate(32)
    
    // Print the blocks persistent ID (the first block is guaranteed to have an ID of 0)
    println(b.Id)
    
    // Copy some text into the block
    copy(b.Bytes, []byte("hello world"))

Open the heap and retrieve the created block:

    h, err := vheap.Open("hello.vheap")
    defer h.Close()
    b := h.GetBlock(0)
    println(string(b.Bytes))

## Details

The storage consists of a single file partitioned into multiple regions. Each
region is mmapped. When a region fills up, a new region is appended. Blocks are
allocated from the first region with sufficient space.

*Currently, deallocated blocks are not reclaimed. This will lead to heavy
fragmentation if blocks are frequently freed.*

A region size is specified at index creation, typically in the order of 32-128MB. Total index size is increased by the region size when an allocation does not fit in any existing region. Additionally, if the new allocation exceeds the region size, the new region will be expanded to fit the allocation.

## On-disk Structure

The structure at the start of each region is:

    // Region signature
    signature [8]byte
    // Pointer to start of free memory:
    freeListPointer int64
    // Size, in bytes, of each region, including header and block list.
    regionSize int64
    // Region ID, starting at 0.
    regionId int64
    // Data
    data []byte

The block list is located at the end of each region, growing down:

    // Next free block ID in this region
    id int64
    // Array of offset and raw size of allocated blocks.
    entries []struct{offset, size int64}

When the top of the heap reaches the bottom of the index, a new region is
allocated.

## Concurrent Access

Allocations are (almost) atomic on 64-bit machines. Almost atomic in that free space is allocated first, then the list of block IDs is updated. If termination occurs in between these two operations, free space will be lost, though the heap will remain consistent.

The implication of this consistency is that a storage block can be read from concurrently while being written to by another process, though currently there is no support for automatically opening appended regions in readers. 
