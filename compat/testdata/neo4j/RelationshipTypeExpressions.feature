Feature: RelationshipTypeExpressions - Neo4j relationship type expressions

  Background:
    Given an empty graph
    And having executed:
      """
      CREATE (a {n: 'a'})-[:R1 {k: 1}]->(b {n: 'b'}),
             (b)-[:R2 {k: 2}]->(c {n: 'c'}),
             (c)-[:R3 {k: 3}]->(d {n: 'd'})
      """

  Scenario: [1] Disjunction of relationship types
    When executing query:
      """
      MATCH ()-[r:R1|R2]->() RETURN r.k AS k
      """
    Then the result should be, in any order:
      | k |
      | 1 |
      | 2 |
    And no side effects

  Scenario: [2] Negated relationship type
    When executing query:
      """
      MATCH ()-[r:!R1]->() RETURN r.k AS k
      """
    Then the result should be, in any order:
      | k |
      | 2 |
      | 3 |
    And no side effects

  Scenario: [3] Relationship type wildcard
    When executing query:
      """
      MATCH ()-[r:%]->() RETURN count(r) AS c
      """
    Then the result should be, in any order:
      | c |
      | 3 |
    And no side effects

  Scenario: [4] Parenthesised relationship type expression
    When executing query:
      """
      MATCH ()-[r:(R1|R3)&!R3]->() RETURN r.k AS k
      """
    Then the result should be, in any order:
      | k |
      | 1 |
    And no side effects

  Scenario: [5] Relationship type expression in a variable-length pattern
    When executing query:
      """
      MATCH ({n: 'a'})-[:R1|R2*1..2]->(x) RETURN x.n AS n
      """
    Then the result should be, in any order:
      | n   |
      | 'b' |
      | 'c' |
    And no side effects
