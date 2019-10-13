/*
 * Copyright 2019 Dgraph Labs, Inc. and Contributors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package table

import (
	"github.com/dgraph-io/badger/y"
	"github.com/pkg/errors"
)

// MergeIterator merges multiple iterators.
// NOTE: MergeIterator owns the array of iterators and is responsible for closing them.
type MergeIterator struct {
	// Small is always the smallest node in the tree.
	small node
	// Big is the other node in the tree. It might be the biggest, but it's not necessary.
	big node

	// When the two iterators have the same value, we pick the value in the first iterator by
	// forwarding the second iterator. See TestMergeIteratorDuplicate
	// On level 0, we can have multiple iterators with the same key. In this case we want to
	// use value of the iterator that was added first to the merge iterator. Second keeps track of the
	// iterator that was added second so that we can resolve the same key conflict.
	second  y.Iterator
	reverse bool
}

type node struct {
	valid bool
	key   []byte
	iter  y.Iterator

	// The two iterators are type asserted from `y.Iterator`, used to inline more function calls.
	// Calling functions on concrete types is much faster (about 25-30%) than calling the
	// interface's function.
	merge  *MergeIterator
	concat *ConcatIterator
}

func (n *node) setIterator(iter y.Iterator) {
	n.iter = iter
	n.merge, _ = iter.(*MergeIterator)
	n.concat, _ = iter.(*ConcatIterator)
}

func (n *node) setKey() {
	if n.merge != nil {
		n.valid = n.merge.small.valid
		if n.valid {
			n.key = n.merge.small.key
		}
	} else if n.concat != nil {
		n.valid = n.concat.Valid()
		if n.valid {
			n.key = n.concat.Key()
		}
	} else {
		n.valid = n.iter.Valid()
		if n.valid {
			n.key = n.iter.Key()
		}
	}
}

func (n *node) next() {
	if n.merge != nil {
		n.merge.Next()
	} else if n.concat != nil {
		n.concat.Next()
	} else {
		n.iter.Next()
	}
	n.setKey()
}

func (n *node) rewind() {
	n.iter.Rewind()
	n.setKey()
}

func (n *node) seek(key []byte) {
	n.iter.Seek(key)
	n.setKey()
}

func (mi *MergeIterator) fix() {
	if !mi.big.valid {
		return
	}
	for mi.small.valid {
		cmp := y.CompareKeys(mi.small.key, mi.big.key)
		// Both the keys are equal.
		if cmp == 0 {
			// Key conflict. Ignore the value in second iterator.
			mi.second.Next()
			var secondValid bool
			if mi.second == mi.small.iter {
				mi.small.setKey()
				secondValid = mi.small.valid
			} else if mi.second == mi.big.iter {
				mi.big.setKey()
				secondValid = mi.big.valid
			} else {
				panic("mi.second invalid")
			}
			if !secondValid {
				// Swap small and big only if second points to
				// the small one and the big is valid.
				if mi.second == mi.small.iter && mi.big.valid {
					mi.swap()
				}
				return
			}
			continue
		}
		if mi.reverse {
			if cmp < 0 {
				mi.swap()
			}
		} else {
			if cmp > 0 {
				mi.swap()
			}
		}
		return
	}
	mi.swap()
}

func (mi *MergeIterator) swap() {
	mi.small, mi.big = mi.big, mi.small
}

// Next returns the next element.
func (mi *MergeIterator) Next() {
	mi.small.next()
	mi.fix()
}

// Rewind seeks to first element (or last element for reverse iterator).
func (mi *MergeIterator) Rewind() {
	mi.small.rewind()
	mi.big.rewind()
	mi.fix()
}

// Seek brings us to element with key >= given key.
func (mi *MergeIterator) Seek(key []byte) {
	mi.small.seek(key)
	mi.big.seek(key)
	mi.fix()
}

// Valid returns whether the MergeIterator is at a valid element.
func (mi *MergeIterator) Valid() bool {
	return mi.small.valid
}

// Key returns the key associated with the current iterator.
func (mi *MergeIterator) Key() []byte {
	return mi.small.key
}

// Value returns the value associated with the iterator.
func (mi *MergeIterator) Value() y.ValueStruct {
	return mi.small.iter.Value()
}

// Close implements y.Iterator.
func (mi *MergeIterator) Close() error {
	err1 := mi.small.iter.Close()
	err2 := mi.big.iter.Close()
	if err1 != nil {
		return errors.Wrap(err1, "MergeIterator")
	}
	return errors.Wrap(err2, "MergeIterator")
}

// NewMergeIterator creates a merge iterator.
func NewMergeIterator(iters []y.Iterator, reverse bool) y.Iterator {
	if len(iters) == 0 {
		return nil
	} else if len(iters) == 1 {
		return iters[0]
	} else if len(iters) == 2 {
		mi := &MergeIterator{
			second:  iters[1],
			reverse: reverse,
		}
		mi.small.setIterator(iters[0])
		mi.big.setIterator(iters[1])
		return mi
	}
	mid := len(iters) / 2
	return NewMergeIterator(
		[]y.Iterator{
			NewMergeIterator(iters[:mid], reverse),
			NewMergeIterator(iters[mid:], reverse),
		}, reverse)
}