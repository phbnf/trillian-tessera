# POSIX Design

This document describes how the storage implementation for running Tessera on a POSIX-compliant filesystem
is intended to work.

## Overview

POSIX provides for a small number of atomic operations on compliant filesystems.

This design leverages those to safely maintain a Merkle tree log on disk, in a format
which can be exposed directly via a read-only endpoint to clients of the log (for example,
using `nginx` or similar).

In contrast with some of other other storage backends, sequencing and integration of entries into
the tree is synchronous.

## Life of a leaf

In the description below, when we talk about writing to files - either appending or creating new ones,
the _actual_ process used always follows the following pattern:
1. Create a temporary file on the same filesystem as the target location
1. If we're appending data, copy the contents of the prefix location into the temporary file
1. Write any new/additional data into the temporary file
1. Close the temporary file
1. Rename the temporary file into the target location

The final step in the dance above is atomic according to the POSIX spec, so in performing this sequence
of actions we can avoid corrupt or partially written files being part of the tree.

1. Leaves are submitted by the binary built using Tessera via a call the storage's `Add` func.
1. The storage library batches these entries up, and, after a configurable period of time has elapsed
   or the batch reaches a configurable size threshold, the batch is sequenced and integrated into the tree:
   1. An advisory lock is taken on a file adjacent to the `checkpoint` file
      This helps prevent multiple frontends from stepping on each other, but isn't necesary for safety.
   1. Flushed entries are assigned contiguous sequence numbers, and written out into entry bundle files.
   1. Integrate newly added leaves into Merkle tree, and write tiles out as files.
   1. Update `checkpoint` file with new state.

## Filesystems

This implementation has been somewhat tested on both a local `ext4` filesystem and on a distributed
[CephFS](https://docs.ceph.com/en/reef/cephfs/) instance on GCP, in both cases with multiple
personality binaries attempting to add new entries concurrently.