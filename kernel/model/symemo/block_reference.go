// SiYuan - Refactor your thinking
// Copyright (c) 2020-present, b3log.org
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package symemo

import "context"

type BlockReferenceReader interface {
	LookupMany(ctx context.Context, blockIDs []string) (map[string]BlockReferenceResolution, error)
	Load(ctx context.Context, blockID string) (BlockReferenceResolution, error)
}

type BlockReferenceResolution struct {
	BlockID           string
	Status            MaterialSourceStatus
	CurrentNotebookID string
	CurrentPath       string
	Encrypted         bool
}

type unresolvedBlockReferenceReader struct{}

func (unresolvedBlockReferenceReader) LookupMany(_ context.Context, blockIDs []string) (map[string]BlockReferenceResolution, error) {
	resolved := make(map[string]BlockReferenceResolution, len(blockIDs))
	for _, blockID := range blockIDs {
		resolved[blockID] = BlockReferenceResolution{BlockID: blockID, Status: MaterialSourceUnresolved}
	}
	return resolved, nil
}

func (unresolvedBlockReferenceReader) Load(_ context.Context, blockID string) (BlockReferenceResolution, error) {
	return BlockReferenceResolution{BlockID: blockID, Status: MaterialSourceUnresolved}, nil
}
