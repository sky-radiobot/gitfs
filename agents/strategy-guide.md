# Agent Task Strategy Guide

When working on a task, first identify which strategy applies, then follow the corresponding workflow. If the task spans multiple strategies, break it into sub-tasks and apply each strategy to its respective part.

## Strategy Selection

Before starting any task, categorize it:

| Task Type | Key Indicators |
|-----------|----------------|
| **Bug Fix** | Something is broken, unexpected behavior, error reports |
| **Feature (TDD)** | New functionality, user-facing capability, "add X" |
| **Refactoring** | Code quality, restructuring without changing behavior |
| **Performance** | Slow operations, memory issues, optimization requests |

If uncertain which strategy applies, ask the human for clarification before proceeding.

---

## Bug Fixing Strategy

**Goal**: Fix the defect while maintaining existing behavior elsewhere.

### Workflow

1. **Reproduce the bug**
   - Understand the reported behavior
   - Create a minimal reproduction case
   - Write a failing test that captures the bug

2. **Locate the root cause**
   - Trace the code path from symptom to source
   - Avoid assumptions; verify with debugging or logging
   - Document the root cause before fixing

3. **Implement the fix**
   - Make the minimal change that fixes the issue
   - Avoid refactoring or "while I'm here" improvements
   - Ensure the failing test now passes

4. **Verify no regressions**
   - Run the full test suite
   - Manually test related functionality if applicable

5. **Document**
   - Commit message should explain what was broken and why
   - Link to issue if one exists

### Anti-patterns to Avoid

- Fixing symptoms instead of root causes
- Making unrelated changes in the same commit
- Skipping the reproduction test

---

## Feature Development Strategy (TDD)

**Goal**: Add new functionality with confidence through test-driven development.

### Workflow

1. **Understand requirements**
   - Clarify expected behavior with the human if unclear
   - Identify edge cases and error conditions
   - Define acceptance criteria

2. **Write failing tests first**
   - Start with the simplest case
   - Each test should fail for the right reason
   - Tests define the interface before implementation

3. **Implement incrementally**
   - Write minimal code to pass each test
   - Resist the urge to implement ahead of tests
   - Keep the red-green-refactor cycle tight

4. **Refactor within green**
   - Only refactor when all tests pass
   - Improve code structure without changing behavior
   - Run tests after each refactoring step

5. **Integration testing**
   - Test the feature in context with existing code
   - Verify it works with real data/scenarios

6. **Manual verification**
   - For TUI changes: run in fixtures repo (see CLAUDE.md)
   - For API changes: test with actual clients

### Anti-patterns to Avoid

- Writing implementation before tests
- Writing too many tests at once before implementing
- Skipping the refactor step
- Gold-plating (adding unrequested features)

---

## Refactoring Strategy

**Goal**: Improve code structure without changing observable behavior.

### Workflow

1. **Ensure test coverage**
   - Verify existing tests cover the code being refactored
   - Add characterization tests if coverage is insufficient
   - Tests are your safety net; don't skip this

2. **Define the target structure**
   - Identify what makes the current structure problematic
   - Sketch the desired end state
   - Break large refactors into small, safe steps

3. **Make incremental changes**
   - Each commit should leave tests passing
   - Use mechanical transformations where possible (extract, inline, rename)
   - Avoid mixing refactoring with behavior changes

4. **Verify continuously**
   - Run tests after each transformation
   - If tests fail, revert and try a smaller step
   - Keep commits small and reversible

5. **Clean up**
   - Remove dead code
   - Update documentation if interfaces changed
   - Ensure naming is consistent

### Anti-patterns to Avoid

- Refactoring without tests
- Making behavior changes during refactoring
- Big-bang rewrites instead of incremental changes
- Refactoring code you don't understand yet

---

## Performance Improvement Strategy

**Goal**: Measurably improve performance without breaking functionality.

### Workflow

1. **Establish baseline measurements**
   - Profile before optimizing; never guess
   - Identify the actual bottleneck with data
   - Document current performance metrics

2. **Set concrete targets**
   - Define what "fast enough" means
   - Get agreement on acceptable trade-offs
   - Ensure targets are measurable

3. **Analyze the bottleneck**
   - Use profiling tools (pprof, etc.)
   - Understand why the code is slow
   - Consider algorithmic vs. implementation issues

4. **Implement optimization**
   - Change one thing at a time
   - Measure after each change
   - Keep the optimization focused on the bottleneck

5. **Verify correctness**
   - Run full test suite
   - Performance optimizations often introduce subtle bugs
   - Test edge cases thoroughly

6. **Document the improvement**
   - Record before/after measurements
   - Explain the optimization approach
   - Note any trade-offs made

### Anti-patterns to Avoid

- Optimizing without profiling first
- Premature optimization of non-bottlenecks
- Sacrificing readability for marginal gains
- Optimizing before correctness is established

---

## Mixed Tasks

Some tasks combine multiple strategies. Handle them by:

1. **Decompose the task** into distinct sub-tasks
2. **Identify the strategy** for each sub-task
3. **Order by dependencies**: typically Bug Fix > Refactoring > Feature > Performance
4. **Execute sequentially**, completing each strategy before starting the next
5. **Commit separately** to keep history clean and reversible

Example: "Fix the crash and improve response time"
1. First: Bug Fix strategy for the crash
2. Then: Performance strategy for response time
3. Separate commits for each

---

## When Uncertain

If you cannot determine which strategy applies:

1. Summarize your understanding of the task
2. List which strategies might apply and why
3. Ask the human to clarify before proceeding

It's better to ask than to apply the wrong approach.
