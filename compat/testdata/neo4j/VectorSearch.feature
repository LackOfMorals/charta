Feature: VectorSearch - db.index.vector.queryNodes

  Background:
    Given an empty graph
    And having executed:
      """
      CREATE VECTOR INDEX docs FOR (d:Doc) ON (d.embedding)
      OPTIONS {indexConfig: {`vector.dimensions`: 2, `vector.similarity_function`: 'euclidean'}}
      """
    And having executed:
      """
      CREATE (:Doc {name: 'origin', embedding: [0.0, 0.0]}),
             (:Doc {name: 'near',   embedding: [1.0, 0.0]}),
             (:Doc {name: 'far',    embedding: [3.0, 4.0]}),
             (:Other {name: 'unindexed', embedding: [0.0, 0.0]})
      """

  Scenario: [1] The nearest nodes come back best first with their scores
    When executing query:
      """
      CALL db.index.vector.queryNodes('docs', 3, [0.0, 0.0]) YIELD node, score
      RETURN node.name AS name, round(score, 6) AS score
      """
    Then the result should be, in order:
      | name     | score    |
      | 'origin' | 1.0      |
      | 'near'   | 0.5      |
      | 'far'    | 0.038462 |
    And no side effects

  Scenario: [2] k limits the number of results
    When executing query:
      """
      CALL db.index.vector.queryNodes('docs', 1, [0.9, 0.1]) YIELD node
      RETURN node.name AS name
      """
    Then the result should be, in any order:
      | name   |
      | 'near' |
    And no side effects

  Scenario: [3] The result can be filtered and joined like any procedure output
    When executing query:
      """
      CALL db.index.vector.queryNodes('docs', 10, [0.0, 0.0]) YIELD node, score
      WHERE score > 0.1
      RETURN count(node) AS c
      """
    Then the result should be, in any order:
      | c |
      | 2 |
    And no side effects

  Scenario: [4] Writes are visible to the next search, and deletes and updates too
    When executing query:
      """
      CREATE (:Doc {name: 'new', embedding: [0.1, 0.0]})
      """
    Then the result should be empty
    When executing control query:
      """
      CALL db.index.vector.queryNodes('docs', 2, [0.0, 0.0]) YIELD node RETURN node.name AS name
      """
    Then the result should be, in order:
      | name     |
      | 'origin' |
      | 'new'    |
    When executing query:
      """
      MATCH (d:Doc {name: 'origin'}) DELETE d
      """
    Then the result should be empty
    When executing control query:
      """
      CALL db.index.vector.queryNodes('docs', 2, [0.0, 0.0]) YIELD node RETURN node.name AS name
      """
    Then the result should be, in order:
      | name  |
      | 'new' |
      | 'near' |
    When executing query:
      """
      MATCH (d:Doc {name: 'far'}) SET d.embedding = [0.0, 0.0]
      """
    Then the result should be empty
    When executing control query:
      """
      CALL db.index.vector.queryNodes('docs', 1, [0.0, 0.0]) YIELD node RETURN node.name AS name
      """
    Then the result should be, in any order:
      | name  |
      | 'far' |

  Scenario: [5] Removing the label or the property takes a node out of the index
    When executing query:
      """
      MATCH (d:Doc {name: 'origin'}) REMOVE d:Doc
      """
    Then the result should be empty
    When executing control query:
      """
      CALL db.index.vector.queryNodes('docs', 5, [0.0, 0.0]) YIELD node RETURN node.name AS name
      """
    Then the result should be, in order:
      | name   |
      | 'near' |
      | 'far'  |
    When executing query:
      """
      MATCH (d:Doc {name: 'near'}) REMOVE d.embedding
      """
    Then the result should be empty
    When executing control query:
      """
      CALL db.index.vector.queryNodes('docs', 5, [0.0, 0.0]) YIELD node RETURN node.name AS name
      """
    Then the result should be, in any order:
      | name  |
      | 'far' |

  Scenario: [6] A search inside the statement that wrote sees the new data
    When executing query:
      """
      CREATE (:Doc {name: 'inside', embedding: [0.0, 0.1]})
      WITH 1 AS ignore
      CALL db.index.vector.queryNodes('docs', 2, [0.0, 0.0]) YIELD node
      RETURN node.name AS name
      """
    Then the result should be, in order:
      | name     |
      | 'origin' |
      | 'inside' |

  Scenario Outline: [7] Bad arguments are reported
    When executing query:
      """
      CALL db.index.vector.queryNodes(<args>) YIELD node RETURN node
      """
    Then an <class> should be raised at runtime: <code>

    Examples:
      | args                    | class         | code                 |
      | 'nope', 1, [0.0, 0.0]   | ProcedureError | IndexNotFound        |
      | 'docs', 0, [0.0, 0.0]   | ArgumentError | InvalidArgumentValue |
      | 'docs', 1, [0.0]        | ArgumentError | InvalidArgumentValue |
      | 'docs', 1, [0.0, 0.0, 0.0] | ArgumentError | InvalidArgumentValue |

  Scenario: [8] Null arguments give no rows
    When executing query:
      """
      CALL db.index.vector.queryNodes('docs', 3, null) YIELD node RETURN node
      """
    Then the result should be empty

  Scenario: [9] Cosine indexes score by direction
    Given an empty graph
    And having executed:
      """
      CREATE VECTOR INDEX c FOR (d:Doc) ON (d.e) OPTIONS {indexConfig: {`vector.dimensions`: 2}}
      """
    And having executed:
      """
      CREATE (:Doc {name: 'same', e: [10.0, 0.0]}), (:Doc {name: 'orthogonal', e: [0.0, 1.0]}),
             (:Doc {name: 'opposite', e: [-1.0, 0.0]}), (:Doc {name: 'zero', e: [0.0, 0.0]})
      """
    When executing query:
      """
      CALL db.index.vector.queryNodes('c', 4, [1.0, 0.0]) YIELD node, score
      RETURN node.name AS name, round(score, 6) AS score
      """
    Then the result should be, in order:
      | name         | score |
      | 'same'       | 1.0   |
      | 'orthogonal' | 0.5   |
      | 'opposite'   | 0.0   |

  Scenario: [10] SHOW PROCEDURES lists the procedure
    When executing query:
      """
      SHOW PROCEDURES YIELD name WHERE name = 'db.index.vector.queryNodes' RETURN name
      """
    Then the result should be, in any order:
      | name                          |
      | 'db.index.vector.queryNodes'  |
