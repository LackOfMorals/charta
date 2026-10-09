Feature: FilterLetFinish - Cypher 25 FILTER, LET and FINISH clauses

  Scenario: [1] FILTER keeps matching rows
    Given an empty graph
    When executing query:
      """
      UNWIND range(1, 6) AS i FILTER i % 2 = 0 RETURN i
      """
    Then the result should be, in any order:
      | i |
      | 2 |
      | 4 |
      | 6 |
    And no side effects

  Scenario: [2] LET introduces variables
    Given an empty graph
    When executing query:
      """
      UNWIND range(1, 3) AS i LET sq = i * i, label = 'n' + toString(i) RETURN i, sq, label
      """
    Then the result should be, in any order:
      | i | sq | label |
      | 1 | 1  | 'n1'  |
      | 2 | 4  | 'n2'  |
      | 3 | 9  | 'n3'  |
    And no side effects

  Scenario: [3] FINISH returns no rows but runs the updates
    Given an empty graph
    When executing query:
      """
      UNWIND range(1, 3) AS i CREATE (:N {i: i}) FINISH
      """
    Then the result should be empty
    And the side effects should be:
      | +nodes      | 3 |
      | +labels     | 1 |
      | +properties | 3 |

  Scenario: [4] FILTER after MATCH
    Given an empty graph
    And having executed:
      """
      CREATE (:P {age: 20}), (:P {age: 40})
      """
    When executing query:
      """
      MATCH (p:P) FILTER p.age > 30 RETURN p.age AS age
      """
    Then the result should be, in any order:
      | age |
      | 40  |
    And no side effects
