Feature: ExplainProfile - EXPLAIN and PROFILE prefixes

  Scenario: [1] EXPLAIN does not execute the query
    Given an empty graph
    When executing query:
      """
      EXPLAIN CREATE (:N {x: 1})
      """
    Then the result should be empty
    And no side effects

  Scenario: [2] EXPLAIN of a read leaves the graph untouched and returns no rows
    Given an empty graph
    And having executed:
      """
      CREATE (:N {x: 1})
      """
    When executing query:
      """
      EXPLAIN MATCH (n:N) RETURN n.x AS x
      """
    Then the result should be empty
    And no side effects

  Scenario: [3] PROFILE runs the query
    Given an empty graph
    And having executed:
      """
      CREATE (:N {x: 1})
      """
    When executing query:
      """
      PROFILE MATCH (n:N) RETURN n.x AS x
      """
    Then the result should be, in any order:
      | x |
      | 1 |
    And no side effects
