Feature: MapProjection - map projections of nodes, relationships and maps

  Background:
    Given an empty graph
    And having executed:
      """
      CREATE (:Person {name: 'Alice', age: 30})
      """

  Scenario: [1] Property selectors
    When executing query:
      """
      MATCH (n:Person) RETURN n {.name, .age} AS m
      """
    Then the result should be, in any order:
      | m                          |
      | {name: 'Alice', age: 30}   |
    And no side effects

  Scenario: [2] All properties
    When executing query:
      """
      MATCH (n:Person) RETURN n {.*} AS m
      """
    Then the result should be, in any order:
      | m                        |
      | {name: 'Alice', age: 30} |
    And no side effects

  Scenario: [3] Literal and variable entries
    When executing query:
      """
      MATCH (n:Person) WITH n, 5 AS extra RETURN n {.name, extra, double: n.age * 2} AS m
      """
    Then the result should be, in any order:
      | m                                      |
      | {name: 'Alice', extra: 5, double: 60}  |
    And no side effects

  Scenario: [4] All properties plus overrides
    When executing query:
      """
      MATCH (n:Person) RETURN n {.*, age: 31} AS m
      """
    Then the result should be, in any order:
      | m                        |
      | {name: 'Alice', age: 31} |
    And no side effects

  Scenario: [5] Missing property gives null
    When executing query:
      """
      MATCH (n:Person) RETURN n {.name, .missing} AS m
      """
    Then the result should be, in any order:
      | m                           |
      | {name: 'Alice', missing: null} |
    And no side effects

  Scenario: [6] Projection of a map
    When executing query:
      """
      WITH {a: 1, b: 2} AS m RETURN m {.a, c: 3} AS p
      """
    Then the result should be, in any order:
      | p          |
      | {a: 1, c: 3} |
    And no side effects
