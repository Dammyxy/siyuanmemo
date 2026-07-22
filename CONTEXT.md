# SiYuanMemo Learning

SiYuanMemo Learning defines how knowledge material enters, participates in, and moves through a SuperMemo-aligned learning process inside SiYuan.

## Knowledge

**Element**:
A persistent unit of knowledge that has one identity, one lifecycle state, and an optional learning schedule.
_Avoid_: Card, note

**Item**:
An Element practiced through active recall and a grade from `0` through `5`.
_Avoid_: Flashcard when referring to all Elements

**Topic**:
An Element reviewed without a recall grade and advanced through `Next` during learning.
_Avoid_: Article when referring to its learning identity

**Concept**:
A semantic Element used to bind and organize knowledge without participating in learning unless explicitly enrolled.
_Avoid_: Folder, tag

## Lifecycle

**Pending**:
An Element waiting in the ordered new-material reservoir and not yet carrying an active learning schedule.
_Avoid_: New, unscheduled

**Memorized**:
An Element participating in the learning process with an active schedule, regardless of whether it is currently due.
_Avoid_: Remembered, mastered

**Dismissed**:
An Element excluded from learning while its material, history, and previously adopted schedule remain preserved.
_Avoid_: Deleted, suspended

**Introduction**:
The transition that moves a Pending Element into the Memorized lifecycle and initializes its schedule.
_Avoid_: First review

## Daily learning

**Learning Day**:
The collection-wide dated learning period determined by its time zone and Midnight Shift rather than by civil midnight alone.
_Avoid_: Calendar day, local day

**Midnight Shift**:
The collection setting that moves the start of a Learning Day by a whole number of hours after civil midnight.
_Avoid_: Card due time

**Outstanding Element**:
A Memorized Element whose scheduled date has reached the current Learning Day.
_Avoid_: Queue member

**Outstanding Queue**:
The current Learning Day's ordered learning population, containing Outstanding Elements and any explicit temporary additions.
_Avoid_: Due list

**Pending Queue**:
The collection-wide relative order in which Pending Elements are introduced as new material.
_Avoid_: New-card queue

**Priority Order**:
The collection-wide relative importance order of all Elements, where an earlier position and lower displayed percentage mean higher priority.
_Avoid_: Weight, score

## Learning operations

**Repetition**:
An accepted schedule-changing processing of an Element through its target-native learning action.
_Avoid_: Navigation, drill

**Mid-interval Repetition**:
A deliberate Repetition performed before a Memorized Element becomes Outstanding.
_Avoid_: Preview, drill

**Final Drill**:
Schedule-neutral repeated practice of Items admitted after a grade below Good until they receive Good or better.
_Avoid_: Repetition queue, retrieval-practice queue

**Subset Learning**:
Learning over an explicitly selected ordered set of Elements while preserving global lifecycle and scheduling semantics.
_Avoid_: Separate scheduler

**Neural Roam**:
A graph-guided exploration workflow whose visits are schedule-neutral unless the learner explicitly invokes an Element's native learning action.
_Avoid_: Random review, queue
