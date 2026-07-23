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
	"fmt"
	"testing"
)

func TestCreateTopicSchedulerPlansCausalRootIntroduction(t *testing.T) {
	engine, _ := newFixtureEngine(t)
	const elementID = "20260723090000-topicaa"
	const eventID = "20260723090100-eventaa"

	event, err := engine.scheduler.planTopicIntroduction(elementID, eventID)
	if err != nil {
		t.Fatal(err)
	}
	if event.Type != "introduceElement" || event.ReviewKind != "introduceTopic" || event.BaseEventID != "" || event.SessionID != "" {
		t.Fatalf("introduction event identity facts = %#v", event)
	}
	if event.TopicPolicyVersion != "siyuanmemo-topic-initial-v1" || event.TopicSeed != topicInitialSeed(eventID, elementID) {
		t.Fatalf("topic initial policy facts = %#v", event)
	}
	if event.TopicInitialIntervalDays < 1 || event.TopicInitialIntervalDays > 15 || event.TopicInitialIntervalDays != topicInitialInterval(event.TopicSeed) || event.TopicNextIntervalDays != event.TopicInitialIntervalDays {
		t.Fatalf("initial interval facts = %#v", event)
	}
	if event.LearningDayID != "2026-07-19" || event.LearningDate != "2026-07-19" || event.After.DueLearningDay != addLearningDays(event.LearningDayID, event.TopicInitialIntervalDays) {
		t.Fatalf("learning day facts = %#v", event)
	}
	if event.Before.LifecycleState != "pending" || event.Before.PriorityPosition != 0 || event.After.LifecycleState != "memorized" || event.After.ScheduleProfile != topicAFactorV1ID || event.After.AcceptedReviewAction != "NextTopic" || event.After.PriorityPosition != 0 {
		t.Fatalf("topic projection facts = %#v -> %#v", event.Before, event.After)
	}
	if event.RawGrade != nil || event.Passed != nil || event.RatingLabel != "" || event.RatingMapping != "" || event.After.LastRawGrade != nil || event.After.LastPassed != nil || event.After.Lapses != 0 {
		t.Fatalf("Topic introduction carried Item review fields: %#v", event)
	}
	state, ok := event.After.AlgorithmStates[topicAFactorV1ID]
	if !ok || state.Algorithm != topicAFactorV1ID || state.SchemaVersion != 1 {
		t.Fatalf("topic state = %#v", event.After.AlgorithmStates)
	}
}

func TestCreateTopicSchedulerInitialIntervalIdentityFixtures(t *testing.T) {
	engine, _ := newFixtureEngine(t)
	seen := map[int]bool{}
	for i := 0; i < 100; i++ {
		elementID := fmt.Sprintf("2026072316%04d-topicaa", i)
		eventID := fmt.Sprintf("2026072317%04d-eventaa", i)
		event, err := engine.scheduler.planTopicIntroduction(elementID, eventID)
		if err != nil {
			t.Fatalf("fixture %d: %v", i, err)
		}
		if event.TopicInitialIntervalDays < 1 || event.TopicInitialIntervalDays > 15 || event.TopicInitialIntervalDays != topicInitialInterval(topicInitialSeed(eventID, elementID)) {
			t.Fatalf("fixture %d interval facts = %#v", i, event)
		}
		if event.TopicNextIntervalDays != event.TopicInitialIntervalDays || event.After.DueLearningDay != addLearningDays(event.LearningDayID, event.TopicInitialIntervalDays) {
			t.Fatalf("fixture %d due facts = %#v", i, event)
		}
		seen[event.TopicInitialIntervalDays] = true
	}
	if len(seen) < 10 {
		t.Fatalf("initial interval fixtures were too concentrated: %#v", seen)
	}
}
