Feature: PathSelectors - ANY, ALL, SHORTEST and SHORTEST ... GROUPS selectors

  Background:
    Given an empty graph
    And having executed:
      """
      CREATE (s {n: 's'}), (a {n: 'a'}), (b {n: 'b'}), (c {n: 'c'}), (d {n: 'd'}), (t {n: 't'}),
             (s)-[:R]->(a), (a)-[:R]->(t),
             (s)-[:R]->(b), (b)-[:R]->(t),
             (s)-[:R]->(c), (c)-[:R]->(d), (d)-[:R]->(t)
      """

  Scenario: [1] ANY SHORTEST returns one shortest path per pair
    When executing query:
      """
      MATCH p = ANY SHORTEST ({n: 's'})-[:R]->+({n: 't'}) RETURN length(p) AS len
      """
    Then the result should be, in any order:
      | len |
      | 2   |
    And no side effects

  Scenario: [2] ALL SHORTEST returns every shortest path
    When executing query:
      """
      MATCH p = ALL SHORTEST ({n: 's'})-[:R]->+({n: 't'}) RETURN length(p) AS len
      """
    Then the result should be, in any order:
      | len |
      | 2   |
      | 2   |
    And no side effects

  Scenario: [3] SHORTEST k returns the k shortest paths
    When executing query:
      """
      MATCH p = SHORTEST 3 ({n: 's'})-[:R]->+({n: 't'}) RETURN length(p) AS len
      """
    Then the result should be, in any order:
      | len |
      | 2   |
      | 2   |
      | 3   |
    And no side effects

  Scenario: [4] SHORTEST 2 stops at two paths
    When executing query:
      """
      MATCH p = SHORTEST 2 ({n: 's'})-[:R]->+({n: 't'}) RETURN length(p) AS len
      """
    Then the result should be, in any order:
      | len |
      | 2   |
      | 2   |
    And no side effects

  Scenario: [5] SHORTEST k GROUPS returns all paths of the k shortest lengths
    When executing query:
      """
      MATCH p = SHORTEST 1 GROUPS ({n: 's'})-[:R]->+({n: 't'}) RETURN length(p) AS len
      """
    Then the result should be, in any order:
      | len |
      | 2   |
      | 2   |
    And no side effects

  Scenario: [6] SHORTEST 2 GROUPS includes the next length
    When executing query:
      """
      MATCH p = SHORTEST 2 GROUPS ({n: 's'})-[:R]->+({n: 't'}) RETURN length(p) AS len
      """
    Then the result should be, in any order:
      | len |
      | 2   |
      | 2   |
      | 3   |
    And no side effects

  Scenario: [7] ANY k returns k paths per pair
    When executing query:
      """
      MATCH p = ANY 2 ({n: 's'})-[:R]->+({n: 't'}) RETURN count(p) AS c
      """
    Then the result should be, in any order:
      | c |
      | 2 |
    And no side effects

  Scenario: [8] ALL returns every path
    When executing query:
      """
      MATCH p = ALL ({n: 's'})-[:R]->+({n: 't'}) RETURN length(p) AS len
      """
    Then the result should be, in any order:
      | len |
      | 2   |
      | 2   |
      | 3   |
    And no side effects

  Scenario: [9] Selectors partition by end node
    When executing query:
      """
      MATCH p = ANY SHORTEST ({n: 's'})-[:R]->+(x) RETURN x.n AS n, length(p) AS len
      """
    Then the result should be, in any order:
      | n   | len |
      | 'a' | 1   |
      | 'b' | 1   |
      | 'c' | 1   |
      | 'd' | 2   |
      | 't' | 2   |
    And no side effects

  Scenario: [10] Selectors over a quantified path pattern
    When executing query:
      """
      MATCH p = SHORTEST 1 ({n: 's'}) ((x)-[:R]->(y))+ ({n: 't'}) RETURN length(p) AS len
      """
    Then the result should be, in any order:
      | len |
      | 2   |
    And no side effects
