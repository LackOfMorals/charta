Feature: LabelExpressions - Neo4j label and relationship type expressions

  Background:
    Given an empty graph
    And having executed:
      """
      CREATE (:A {n: 'a'}), (:B {n: 'b'}), (:A:B {n: 'ab'}), (:C {n: 'c'}), ({n: 'none'}),
             (:A:B:C {n: 'abc'})
      """

  Scenario: [1] Conjunction of labels
    When executing query:
      """
      MATCH (x:A&B) RETURN x.n AS n
      """
    Then the result should be, in any order:
      | n     |
      | 'ab'  |
      | 'abc' |
    And no side effects

  Scenario: [2] Disjunction of labels
    When executing query:
      """
      MATCH (x:A|B) RETURN x.n AS n
      """
    Then the result should be, in any order:
      | n     |
      | 'a'   |
      | 'b'   |
      | 'ab'  |
      | 'abc' |
    And no side effects

  Scenario: [3] Negated label
    When executing query:
      """
      MATCH (x:!A) RETURN x.n AS n
      """
    Then the result should be, in any order:
      | n      |
      | 'b'    |
      | 'c'    |
      | 'none' |
    And no side effects

  Scenario: [4] Wildcard matches any labelled node
    When executing query:
      """
      MATCH (x:%) RETURN x.n AS n
      """
    Then the result should be, in any order:
      | n     |
      | 'a'   |
      | 'b'   |
      | 'ab'  |
      | 'c'   |
      | 'abc' |
    And no side effects

  Scenario: [5] Negated wildcard matches only unlabelled nodes
    When executing query:
      """
      MATCH (x:!%) RETURN x.n AS n
      """
    Then the result should be, in any order:
      | n      |
      | 'none' |
    And no side effects

  Scenario: [6] Conjunction with a negation
    When executing query:
      """
      MATCH (x:A&!B) RETURN x.n AS n
      """
    Then the result should be, in any order:
      | n   |
      | 'a' |
    And no side effects

  Scenario: [7] Negation binds tighter than conjunction
    When executing query:
      """
      MATCH (x:!A&B) RETURN x.n AS n
      """
    Then the result should be, in any order:
      | n   |
      | 'b' |
    And no side effects

  Scenario: [8] Conjunction binds tighter than disjunction
    When executing query:
      """
      MATCH (x:A|B&C) RETURN x.n AS n
      """
    Then the result should be, in any order:
      | n     |
      | 'a'   |
      | 'ab'  |
      | 'abc' |
    And no side effects

  Scenario: [9] Parentheses override precedence
    When executing query:
      """
      MATCH (x:(A|B)&C) RETURN x.n AS n
      """
    Then the result should be, in any order:
      | n     |
      | 'abc' |
    And no side effects

  Scenario: [10] Label expression as a WHERE predicate
    When executing query:
      """
      MATCH (x) WHERE x:A|C RETURN x.n AS n
      """
    Then the result should be, in any order:
      | n     |
      | 'a'   |
      | 'ab'  |
      | 'c'   |
      | 'abc' |
    And no side effects

  Scenario: [11] Negated label expression as a WHERE predicate
    When executing query:
      """
      MATCH (x) WHERE NOT x:A&B RETURN x.n AS n
      """
    Then the result should be, in any order:
      | n      |
      | 'a'    |
      | 'b'    |
      | 'c'    |
      | 'none' |
    And no side effects

  Scenario: [12] Label expression in the legacy colon-separated form still means conjunction
    When executing query:
      """
      MATCH (x:A:B) RETURN x.n AS n
      """
    Then the result should be, in any order:
      | n     |
      | 'ab'  |
      | 'abc' |
    And no side effects
