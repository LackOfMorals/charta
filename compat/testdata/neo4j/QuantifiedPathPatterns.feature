Feature: QuantifiedPathPatterns - quantified path patterns and quantified relationships

  Background:
    Given an empty graph
    And having executed:
      """
      CREATE (n1:S {n: 1})-[:R]->(n2:S {n: 2})-[:R]->(n3:S {n: 3})-[:R]->(n4:S {n: 4})
      """

  Scenario: [1] Exact quantifier
    When executing query:
      """
      MATCH (a {n: 1}) ((x)-[:R]->(y)){2} (z) RETURN z.n AS n
      """
    Then the result should be, in any order:
      | n |
      | 3 |
    And no side effects

  Scenario: [2] Bounded range quantifier
    When executing query:
      """
      MATCH (a {n: 1}) ((x)-[:R]->(y)){1,3} (z) RETURN z.n AS n
      """
    Then the result should be, in any order:
      | n |
      | 2 |
      | 3 |
      | 4 |
    And no side effects

  Scenario: [3] Plus quantifier
    When executing query:
      """
      MATCH (a {n: 2}) ((x)-[:R]->(y))+ (z) RETURN z.n AS n
      """
    Then the result should be, in any order:
      | n |
      | 3 |
      | 4 |
    And no side effects

  Scenario: [4] Star quantifier allows zero iterations
    When executing query:
      """
      MATCH (a {n: 3}) ((x)-[:R]->(y))* (z) RETURN z.n AS n
      """
    Then the result should be, in any order:
      | n |
      | 3 |
      | 4 |
    And no side effects

  Scenario: [5] Group variables are lists with one entry per iteration
    When executing query:
      """
      MATCH (a {n: 1}) ((x)-[:R]->(y)){3} (b)
      RETURN [v IN x | v.n] AS xs, [v IN y | v.n] AS ys, b.n AS b
      """
    Then the result should be, in any order:
      | xs        | ys        | b |
      | [1, 2, 3] | [2, 3, 4] | 4 |
    And no side effects

  Scenario: [6] WHERE inside the group applies to every iteration
    When executing query:
      """
      MATCH (a {n: 1}) ((x)-[:R]->(y) WHERE y.n < 4)+ (b) RETURN b.n AS n
      """
    Then the result should be, in any order:
      | n |
      | 2 |
      | 3 |
    And no side effects

  Scenario: [7] Path variable covers every iteration
    When executing query:
      """
      MATCH p = (a {n: 1}) ((x)-[:R]->(y)){2} (b) RETURN length(p) AS len, b.n AS n
      """
    Then the result should be, in any order:
      | len | n |
      | 2   | 3 |
    And no side effects

  Scenario: [8] Quantified relationship shorthand with an exact count
    When executing query:
      """
      MATCH (a {n: 1})-[:R]->{2}(z) RETURN z.n AS n
      """
    Then the result should be, in any order:
      | n |
      | 3 |
    And no side effects

  Scenario: [9] Quantified relationship shorthand with a range
    When executing query:
      """
      MATCH (a {n: 1})-[:R]->{1,2}(z) RETURN z.n AS n
      """
    Then the result should be, in any order:
      | n |
      | 2 |
      | 3 |
    And no side effects

  Scenario: [10] Quantified relationship shorthand with + and *
    When executing query:
      """
      MATCH (a {n: 1})-[:R]->+(z) WITH collect(z.n) AS plus
      MATCH (a {n: 1})-[:R]->*(z) RETURN plus, collect(z.n) AS star
      """
    Then the result should be, in any order:
      | plus      | star         |
      | [2, 3, 4] | [1, 2, 3, 4] |
    And no side effects

  Scenario: [11] The relationship variable of a quantified relationship is a list
    When executing query:
      """
      MATCH (a {n: 1})-[r:R]->{2}(z) RETURN size(r) AS hops
      """
    Then the result should be, in any order:
      | hops |
      | 2    |
    And no side effects

  Scenario: [12] Quantified group at the start of a pattern
    When executing query:
      """
      MATCH ((x)-[:R]->(y)){2} RETURN count(*) AS c
      """
    Then the result should be, in any order:
      | c |
      | 2 |
    And no side effects

  Scenario: [13] Relationships are not reused, so cycles terminate
    Given an empty graph
    And having executed:
      """
      CREATE (c1 {n: 1})-[:R]->(c2 {n: 2})-[:R]->(c1)
      """
    When executing query:
      """
      MATCH ({n: 1}) ((x)-[:R]->(y))+ (z) RETURN z.n AS n
      """
    Then the result should be, in any order:
      | n |
      | 2 |
      | 1 |
    And no side effects

  Scenario: [14] An unquantified parenthesised pattern keeps singleton variables
    When executing query:
      """
      MATCH (a {n: 1}) ((x)-[:R]->(y)) (b) RETURN x.n AS x, y.n AS y, b.n AS b
      """
    Then the result should be, in any order:
      | x | y | b |
      | 1 | 2 | 2 |
    And no side effects

  Scenario: [15] Two quantified groups in a row
    When executing query:
      """
      MATCH (a {n: 1}) ((x)-[:R]->(y)){1} ((u)-[:R]->(v)){2} (b) RETURN b.n AS n
      """
    Then the result should be, in any order:
      | n |
      | 4 |
    And no side effects

  Scenario: [16] Aggregating a group variable
    When executing query:
      """
      MATCH (a {n: 1}) ((x)-[:R]->(y)){1,3} (b)
      RETURN b.n AS n, size(x) AS hops, reduce(s = 0, v IN y | s + v.n) AS total
      """
    Then the result should be, in any order:
      | n | hops | total |
      | 2 | 1    | 2     |
      | 3 | 2    | 5     |
      | 4 | 3    | 9     |
    And no side effects

  Scenario: [17] Node label filters inside the group
    Given an empty graph
    And having executed:
      """
      CREATE (:A {n: 1})-[:R]->(:B {n: 2})-[:R]->(:B {n: 3})-[:R]->(:C {n: 4})
      """
    When executing query:
      """
      MATCH (:A) ((:!C)-[:R]->(y:B))+ (z) RETURN z.n AS n
      """
    Then the result should be, in any order:
      | n |
      | 2 |
      | 3 |
    And no side effects
