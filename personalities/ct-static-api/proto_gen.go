// Copyright 2024 The Tessera authors. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package ctfe

//go:generate sh -c "protoc -I=. -I$(go list -f '{{ .Dir }}' github.com/google/trillian) -I$(go list -f '{{ .Dir }}' github.com/transparency-dev/trillian-tessera/personalities/ct-static-api) --go_out=paths=source_relative:. configpb/config.proto"
