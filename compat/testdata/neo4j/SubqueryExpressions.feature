Feature: SubqueryExpressions - EXISTS, COUNT and COLLECT subqueries

  Background:
    Given an empty graph
    And having executed:
      """
      CREATE (a:Person {name: 'Alice'}), (b:Person {name: 'Bob'}), (c:Person {name: 'Carol'}),
             (a)-[:KNOWS]->(b), (a)-[:KNOWS]->(c), (b)-[:KNOWS]->(c)
      """

  Scenario: [1] EXISTS with a pattern
    When executing query:
      """
      MATCH (p:Person) WHERE EXISTS { (p)-[:KNOWS]->() } RETURN p.name AS name
      """
    Then the result should be, in any order:
      | name    |
      | 'Alice' |
      | 'Bob'   |
    And no side effects

  Scenario: [2] EXISTS with a full subquery and its own WHERE
    When executing query:
      """
      MATCH (p:Person)
      WHERE EXISTS {
        MATCH (p)-[:KNOWS]->(f)
        WHERE f.name STARTS WITH 'C'
      }
      RETURN p.name AS name
      """
    Then the result should be, in any order:
      | name    |
      | 'Alice' |
      | 'Bob'   |
    And no side effects

  Scenario: [3] NOT EXISTS
    When executing query:
      """
      MATCH (p:Person) WHERE NOT EXISTS { (p)-[:KNOWS]->() } RETURN p.name AS name
      """
    Then the result should be, in any order:
      | name    |
      | 'Carol' |
    And no side effects

  Scenario: [4] COUNT subquery in RETURN
    When executing query:
      """
      MATCH (p:Person) RETURN p.name AS name, COUNT { (p)-[:KNOWS]->() } AS friends
      """
    Then the result should be, in any order:
      | name    | friends |
      | 'Alice' | 2       |
      | 'Bob'   | 1       |
      | 'Carol' | 0       |
    And no side effects

  Scenario: [5] COUNT subquery in WHERE
    When executing query:
      """
      MATCH (p:Person) WHERE COUNT { (p)-[:KNOWS]->() } >= 2 RETURN p.name AS name
      """
    Then the result should be, in any order:
      | name    |
      | 'Alice' |
    And no side effects

  Scenario: [6] COLLECT subquery
    When executing query:
      """
      MATCH (p:Person)
      RETURN p.name AS name, COLLECT { MATCH (p)-[:KNOWS]->(f) RETURN f.name ORDER BY f.name } AS friends
      """
    Then the result should be, in any order:
      | name    | friends          |
      | 'Alice' | ['Bob', 'Carol'] |
      | 'Bob'   | ['Carol']        |
      | 'Carol' | []               |
    And no side effects

  Scenario: [7] Nested EXISTS
    When executing query:
      """
      MATCH (p:Person)
      WHERE EXISTS { MATCH (p)-[:KNOWS]->(f) WHERE EXISTS { (f)-[:KNOWS]->() } }
      RETURN p.name AS name
      """
    Then the result should be, in any order:
      | name    |
      | 'Alice' |
    And no side effects
