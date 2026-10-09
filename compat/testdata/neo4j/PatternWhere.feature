Feature: PatternWhere - WHERE inside node and relationship patterns, OFFSET

  Background:
    Given an empty graph
    And having executed:
      """
      CREATE (a:Person {name: 'Alice', age: 30}), (b:Person {name: 'Bob', age: 25}), (c:Person {name: 'Carol', age: 40}),
             (a)-[:KNOWS {since: 2010}]->(b), (a)-[:KNOWS {since: 2020}]->(c)
      """

  Scenario: [1] WHERE in a node pattern
    When executing query:
      """
      MATCH (p:Person WHERE p.age > 26) RETURN p.name AS name
      """
    Then the result should be, in any order:
      | name    |
      | 'Alice' |
      | 'Carol' |
    And no side effects

  Scenario: [2] WHERE in a relationship pattern
    When executing query:
      """
      MATCH (a)-[r:KNOWS WHERE r.since > 2015]->(b) RETURN b.name AS name
      """
    Then the result should be, in any order:
      | name    |
      | 'Carol' |
    And no side effects

  Scenario: [3] WHERE in node patterns of a path
    When executing query:
      """
      MATCH (a:Person WHERE a.age < 35)-[:KNOWS]->(b:Person WHERE b.age < 35) RETURN a.name AS a, b.name AS b
      """
    Then the result should be, in any order:
      | a       | b     |
      | 'Alice' | 'Bob' |
    And no side effects

  Scenario: [4] OFFSET is a synonym for SKIP
    When executing query:
      """
      MATCH (p:Person) RETURN p.name AS name ORDER BY name OFFSET 1 LIMIT 1
      """
    Then the result should be, in order:
      | name  |
      | 'Bob' |
    And no side effects

  Scenario: [5] LIMIT before OFFSET is accepted
    When executing query:
      """
      MATCH (p:Person) RETURN p.name AS name ORDER BY name LIMIT 2 OFFSET 1
      """
    Then the result should be, in order:
      | name    |
      | 'Bob'   |
      | 'Carol' |
    And no side effects
