package analyzer

import "sort"

// orderChangedFunctions ranks changed functions by their downstream hierarchy.
// Operations (Pure): derives review order and leaf counts from an immutable graph.
func orderChangedFunctions(snapshot GitSnapshot, outgoing map[string][]Edge) GitSnapshot {
	if len(snapshot.ChangedFunctions) == 0 {
		return snapshot
	}

	changedByID := make(map[string]ChangedFunction, len(snapshot.ChangedFunctions))
	for _, function := range snapshot.ChangedFunctions {
		changedByID[function.ID] = function
	}

	reachable := changedReachability(snapshot.ChangedFunctions, changedByID, outgoing)
	components, componentByID := changedComponents(snapshot.ChangedFunctions, reachable)
	componentEdges := make([]map[int]bool, len(components))
	indegree := make([]int, len(components))
	for index := range componentEdges {
		componentEdges[index] = make(map[int]bool)
	}
	for sourceID, targets := range reachable {
		sourceComponent := componentByID[sourceID]
		for targetID := range targets {
			targetComponent := componentByID[targetID]
			if sourceComponent == targetComponent || componentEdges[sourceComponent][targetComponent] {
				continue
			}
			componentEdges[sourceComponent][targetComponent] = true
			indegree[targetComponent]++
		}
	}

	leafCounts := make([]int, len(components))
	for source, targets := range componentEdges {
		for target := range targets {
			if len(componentEdges[target]) == 0 {
				leafCounts[source] += len(components[target])
			}
		}
	}

	ordered := make([]ChangedFunction, 0, len(snapshot.ChangedFunctions))
	frontier := zeroIndegreeComponents(indegree)
	for len(frontier) > 0 {
		sort.Slice(frontier, func(left int, right int) bool {
			if leafCounts[frontier[left]] != leafCounts[frontier[right]] {
				return leafCounts[frontier[left]] > leafCounts[frontier[right]]
			}
			return componentLess(components[frontier[left]], components[frontier[right]], changedByID)
		})

		next := make([]int, 0)
		for _, component := range frontier {
			members := append([]string(nil), components[component]...)
			sort.Slice(members, func(left int, right int) bool {
				return changedFunctionLess(changedByID[members[left]], changedByID[members[right]])
			})
			for _, id := range members {
				function := changedByID[id]
				function.LeafDescendantCount = leafCounts[component]
				ordered = append(ordered, function)
			}
			for target := range componentEdges[component] {
				indegree[target]--
				if indegree[target] == 0 {
					next = append(next, target)
				}
			}
		}
		frontier = next
	}

	snapshot.ChangedFunctions = ordered
	return snapshot
}

func changedReachability(changed []ChangedFunction, changedByID map[string]ChangedFunction, outgoing map[string][]Edge) map[string]map[string]bool {
	reachable := make(map[string]map[string]bool, len(changed))
	for _, source := range changed {
		reachable[source.ID] = make(map[string]bool)
		visited := map[string]bool{source.ID: true}
		frontier := []string{source.ID}
		for len(frontier) > 0 {
			current := frontier[len(frontier)-1]
			frontier = frontier[:len(frontier)-1]
			for _, edge := range outgoing[current] {
				if _, isChanged := changedByID[edge.CalleeID]; isChanged && edge.CalleeID != source.ID {
					reachable[source.ID][edge.CalleeID] = true
				}
				if !visited[edge.CalleeID] {
					visited[edge.CalleeID] = true
					frontier = append(frontier, edge.CalleeID)
				}
			}
		}
	}
	return reachable
}

func changedComponents(changed []ChangedFunction, reachable map[string]map[string]bool) ([][]string, map[string]int) {
	components := make([][]string, 0)
	componentByID := make(map[string]int, len(changed))
	assigned := make(map[string]bool, len(changed))
	for _, source := range changed {
		if assigned[source.ID] {
			continue
		}
		component := []string{source.ID}
		assigned[source.ID] = true
		for _, candidate := range changed {
			if assigned[candidate.ID] || !reachable[source.ID][candidate.ID] || !reachable[candidate.ID][source.ID] {
				continue
			}
			assigned[candidate.ID] = true
			component = append(component, candidate.ID)
		}
		componentIndex := len(components)
		for _, id := range component {
			componentByID[id] = componentIndex
		}
		components = append(components, component)
	}
	return components, componentByID
}

func zeroIndegreeComponents(indegree []int) []int {
	result := make([]int, 0)
	for component, count := range indegree {
		if count == 0 {
			result = append(result, component)
		}
	}
	return result
}

func componentLess(left []string, right []string, changedByID map[string]ChangedFunction) bool {
	leftFunctions := make([]ChangedFunction, 0, len(left))
	for _, id := range left {
		leftFunctions = append(leftFunctions, changedByID[id])
	}
	rightFunctions := make([]ChangedFunction, 0, len(right))
	for _, id := range right {
		rightFunctions = append(rightFunctions, changedByID[id])
	}
	sort.Slice(leftFunctions, func(first int, second int) bool {
		return changedFunctionLess(leftFunctions[first], leftFunctions[second])
	})
	sort.Slice(rightFunctions, func(first int, second int) bool {
		return changedFunctionLess(rightFunctions[first], rightFunctions[second])
	})
	return changedFunctionLess(leftFunctions[0], rightFunctions[0])
}

func changedFunctionLess(left ChangedFunction, right ChangedFunction) bool {
	if left.QualifiedName != right.QualifiedName {
		return left.QualifiedName < right.QualifiedName
	}
	if left.File != right.File {
		return left.File < right.File
	}
	return left.Line < right.Line
}
