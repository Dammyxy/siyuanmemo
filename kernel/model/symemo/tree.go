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

import (
	"context"
	"sort"
)

func buildElementTree(records map[string]elementSourceRecord, projections map[string]SchedulingProjection, includeSchedule bool) []ElementTreeNode {
	nodes := make(map[string]ElementTreeNode, len(records))
	children := map[string][]elementSourceRecord{}
	var roots []elementSourceRecord
	for _, record := range records {
		node := treeNodeFromRecord(record, projections[record.Element.ID], includeSchedule)
		nodes[record.Element.ID] = node
		if record.ParentID == "" {
			roots = append(roots, record)
			continue
		}
		children[record.ParentID] = append(children[record.ParentID], record)
	}
	var attach func(elementID string) ElementTreeNode
	attach = func(elementID string) ElementTreeNode {
		node := nodes[elementID]
		childRecords := children[elementID]
		sortElementRecords(childRecords)
		for _, child := range childRecords {
			if _, ok := nodes[child.Element.ID]; !ok {
				continue
			}
			node.Children = append(node.Children, attach(child.Element.ID))
		}
		return node
	}
	sortElementRecords(roots)
	tree := make([]ElementTreeNode, 0, len(roots))
	for _, root := range roots {
		tree = append(tree, attach(root.Element.ID))
	}
	return tree
}

func treeNodeFromRecord(record elementSourceRecord, projection SchedulingProjection, includeSchedule bool) ElementTreeNode {
	element := record.Element
	node := ElementTreeNode{
		ElementID:                element.ID,
		Type:                     element.Type,
		Title:                    element.Title,
		ProcessingState:          element.ProcessingState,
		ParentElementID:          record.ParentID,
		RootElementID:            record.RootID,
		StorageKind:              record.StorageKind,
		SourcePath:               record.SourcePath,
		SortRank:                 record.SortRank,
		SourceMode:               sourceMode(element),
		SupportStatus:            supportStatus(element),
		MaterialSourceDiagnostic: record.Material,
	}
	if element.Type == "topic" && element.Payload.Material != nil && element.Payload.Material.Kind == "siyuanBlock" {
		node.BlockID = element.Payload.Material.BlockID
		node.SourceNotebookID = element.Payload.Material.SourceNotebookID
		if record.Material == nil {
			status := MaterialSourceUnresolved
			node.MaterialSourceStatus = &status
		}
	}
	if includeSchedule {
		node.ScheduleSummary = scheduleSummary(element, projection)
	}
	return node
}

func projectedTreeNode(nodes []ElementTreeNode, elementID string) (ElementTreeNode, bool) {
	for _, node := range nodes {
		if node.ElementID == elementID {
			return node, true
		}
		if found, ok := projectedTreeNode(node.Children, elementID); ok {
			return found, true
		}
	}
	return ElementTreeNode{}, false
}

func elementReadView(element Element, node ElementTreeNode) ElementReadView {
	return ElementReadView{
		ElementEnvelope:          ElementEnvelope(element),
		ParentElementID:          node.ParentElementID,
		RootElementID:            node.RootElementID,
		StorageKind:              node.StorageKind,
		SourcePath:               node.SourcePath,
		SourceMode:               node.SourceMode,
		SupportStatus:            node.SupportStatus,
		BlockID:                  node.BlockID,
		SourceNotebookID:         node.SourceNotebookID,
		CurrentNotebookID:        node.CurrentNotebookID,
		CurrentPath:              node.CurrentPath,
		MaterialSourceStatus:     node.MaterialSourceStatus,
		MaterialSourceDiagnostic: node.MaterialSourceDiagnostic,
	}
}

func overlayElementBlockReference(ctx context.Context, reader BlockReferenceReader, view ElementReadView) (ElementReadView, error) {
	if view.BlockID == "" || view.MaterialSourceDiagnostic != nil {
		return view, nil
	}
	resolution, err := reader.Load(ctx, view.BlockID)
	if err != nil {
		return ElementReadView{}, err
	}
	view.CurrentNotebookID = resolution.CurrentNotebookID
	view.CurrentPath = resolution.CurrentPath
	if resolution.Encrypted {
		view.MaterialSourceStatus = nil
		view.MaterialSourceDiagnostic = &ElementMaterialDiagnostic{Code: materialEncrypted, Reason: "Encrypted notebook sources are not supported."}
		return view, nil
	}
	status := resolution.Status
	if status != MaterialSourceAvailable && status != MaterialSourceUnavailable && status != MaterialSourceUnresolved {
		status = MaterialSourceUnresolved
	}
	view.MaterialSourceStatus = &status
	return view, nil
}

func sourceMode(element Element) SourceMode {
	if element.Type == "topic" && element.Payload.Material != nil {
		switch element.Payload.Material.Kind {
		case "html":
			return SourceModeHTML
		case "siyuanBlock":
			return SourceModeBlock
		}
	}
	if supportStatus(element) == SupportStatusUnsupportedReadOnly {
		return SourceModeOpaque
	}
	return SourceModeUnknown
}

func supportStatus(element Element) SupportStatus {
	if isSupportedElementType(element.Type) {
		return SupportStatusSupported
	}
	return SupportStatusUnsupportedReadOnly
}

func scheduleSummary(element Element, projection SchedulingProjection) *ElementScheduleSummary {
	summary := &ElementScheduleSummary{}
	switch element.Type {
	case "item":
		summary.ScheduleProfile = fsrsV1ID
		summary.AcceptedReviewAction = "GradeItem"
	case "topic":
		summary.ScheduleProfile = "topic-afactor-v1"
		summary.AcceptedReviewAction = "NextTopic"
	case "concept":
		summary.ScheduleProfile = "none"
	default:
		summary.ScheduleProfile = "unknown"
	}
	if projection.ScheduleProfile != "" {
		summary.ScheduleProfile = projection.ScheduleProfile
		if projection.AcceptedReviewAction != "" {
			summary.AcceptedReviewAction = projection.AcceptedReviewAction
		} else {
			summary.AcceptedReviewAction = acceptedReviewActionForProfile(projection.ScheduleProfile)
		}
	}
	if projection.ElementID != "" && summary.ScheduleProfile != "none" {
		summary.LifecycleState = projection.LifecycleState
		summary.DueAt = &projection.DueAt
		summary.PriorityPosition = &projection.PriorityPosition
	}
	return summary
}

func acceptedReviewActionForProfile(profile string) string {
	switch profile {
	case fsrsV1ID, simpleV1ID:
		return "GradeItem"
	case "topic-afactor-v1":
		return "NextTopic"
	default:
		return ""
	}
}

func sortElementRecords(records []elementSourceRecord) {
	useSortRanks := false
	for _, record := range records {
		if record.StorageKind == StorageKindRootDocument {
			useSortRanks = true
			break
		}
	}
	sort.SliceStable(records, func(i, j int) bool {
		left, right := records[i], records[j]
		if useSortRanks {
			if left.SortRank != nil && right.SortRank != nil && *left.SortRank != *right.SortRank {
				return *left.SortRank < *right.SortRank
			}
			if left.SortRank != nil && right.SortRank == nil {
				return true
			}
			if left.SortRank == nil && right.SortRank != nil {
				return false
			}
		}
		if left.StorageKind != right.StorageKind {
			return left.StorageKind == StorageKindInternal
		}
		if left.StorageKind == StorageKindInternal && left.DiscoveryOrder != right.DiscoveryOrder {
			return left.DiscoveryOrder < right.DiscoveryOrder
		}
		if left.SourcePath != right.SourcePath {
			return left.SourcePath < right.SourcePath
		}
		return left.Element.ID < right.Element.ID
	})
}

func overlayBlockReferences(ctx context.Context, reader BlockReferenceReader, nodes []ElementTreeNode) ([]ElementTreeNode, error) {
	var blockIDs []string
	seen := map[string]bool{}
	var collect func([]ElementTreeNode)
	collect = func(items []ElementTreeNode) {
		for _, node := range items {
			if node.BlockID != "" && node.MaterialSourceDiagnostic == nil && !seen[node.BlockID] {
				seen[node.BlockID] = true
				blockIDs = append(blockIDs, node.BlockID)
			}
			collect(node.Children)
		}
	}
	collect(nodes)
	if len(blockIDs) == 0 {
		return nodes, nil
	}
	sort.Strings(blockIDs)
	resolutions, err := reader.LookupMany(ctx, blockIDs)
	if err != nil {
		return nil, err
	}
	var apply func([]ElementTreeNode) []ElementTreeNode
	apply = func(items []ElementTreeNode) []ElementTreeNode {
		out := append([]ElementTreeNode(nil), items...)
		for i := range out {
			if out[i].BlockID != "" && out[i].MaterialSourceDiagnostic == nil {
				resolution, ok := resolutions[out[i].BlockID]
				status := MaterialSourceUnresolved
				if ok {
					if resolution.Status == MaterialSourceAvailable {
						status = MaterialSourceAvailable
					}
					out[i].CurrentNotebookID = resolution.CurrentNotebookID
					out[i].CurrentPath = resolution.CurrentPath
					if resolution.Encrypted {
						out[i].MaterialSourceStatus = nil
						out[i].MaterialSourceDiagnostic = &ElementMaterialDiagnostic{Code: materialEncrypted, Reason: "Encrypted notebook sources are not supported."}
					}
				}
				if out[i].MaterialSourceDiagnostic == nil {
					out[i].MaterialSourceStatus = &status
				}
			}
			out[i].Children = apply(out[i].Children)
		}
		return out
	}
	return apply(nodes), nil
}

func filterTreeRoot(nodes []ElementTreeNode, rootElementID string) []ElementTreeNode {
	if rootElementID == "" {
		return nodes
	}
	for _, node := range nodes {
		if node.ElementID == rootElementID {
			return []ElementTreeNode{node}
		}
		if found := filterTreeRoot(node.Children, rootElementID); len(found) > 0 {
			return found
		}
	}
	return nil
}

func selectScheduleSummaries(nodes []ElementTreeNode, include bool) []ElementTreeNode {
	selected := append([]ElementTreeNode(nil), nodes...)
	for i := range selected {
		if !include {
			selected[i].ScheduleSummary = nil
		}
		selected[i].Children = selectScheduleSummaries(selected[i].Children, include)
	}
	return selected
}
