Feature: Vector - VECTOR values and vector functions

  Scenario Outline: [1] Vector construction and dimension
    Given any graph
    When executing query:
      """
      RETURN vector_dimension_count(<vec>) AS d, valueType(<vec>) AS t
      """
    Then the result should be, in any order:
      | d   | t        |
      | <d> | <type>   |
    And no side effects

    Examples:
      | vec                                   | d | type                                   |
      | vector([1, 2, 3], 3, INTEGER8)        | 3 | 'VECTOR<INTEGER8>(3) NOT NULL'         |
      | vector([1, 2], 2, FLOAT32)            | 2 | 'VECTOR<FLOAT32>(2) NOT NULL'          |
      | vector([1.5, 2.5, 3.5], 3, FLOAT64)   | 3 | 'VECTOR<FLOAT64>(3) NOT NULL'          |
      | vector('[1, 2, 3, 4]', 4, INTEGER64)  | 4 | 'VECTOR<INTEGER64>(4) NOT NULL'        |

  Scenario Outline: [2] Distance metrics
    Given any graph
    When executing query:
      """
      RETURN vector_distance(vector([1, 2, 3], 3, FLOAT64), vector([4, 6, 3], 3, FLOAT64), <metric>) AS d
      """
    Then the result should be, in any order:
      | d        |
      | <result> |
    And no side effects

    Examples:
      | metric            | result |
      | EUCLIDEAN         | 5.0    |
      | EUCLIDEAN_SQUARED | 25.0   |
      | MANHATTAN         | 7.0    |
      | HAMMING           | 2.0    |

  Scenario: [3] Norms
    Given any graph
    When executing query:
      """
      WITH vector([3, 4], 2, FLOAT64) AS v
      RETURN vector_norm(v, EUCLIDEAN) AS e, vector_norm(v, MANHATTAN) AS m
      """
    Then the result should be, in any order:
      | e   | m   |
      | 5.0 | 7.0 |
    And no side effects

  Scenario: [4] Similarity functions
    Given any graph
    When executing query:
      """
      WITH vector([1, 0], 2, FLOAT64) AS a, vector([0, 1], 2, FLOAT64) AS b
      RETURN vector.similarity.cosine(a, a) AS same, vector.similarity.cosine(a, b) AS orthogonal,
             vector.similarity.euclidean(a, a) AS e
      """
    Then the result should be, in any order:
      | same | orthogonal | e   |
      | 1.0  | 0.5        | 1.0 |
    And no side effects

  Scenario: [5] Vectors are stored as properties
    Given an empty graph
    And having executed:
      """
      CREATE (:Doc {id: 1, embedding: vector([1, 2, 3], 3, FLOAT32)}),
             (:Doc {id: 2, embedding: vector([4, 5, 6], 3, FLOAT32)})
      """
    When executing query:
      """
      MATCH (a:Doc {id: 1}), (b:Doc {id: 2})
      RETURN vector_distance(a.embedding, b.embedding, EUCLIDEAN_SQUARED) AS d,
             a.embedding = vector([1, 2, 3], 3, FLOAT32) AS same,
             a.embedding IS :: VECTOR<FLOAT32>(3) AS typed,
             a.embedding IS :: VECTOR<INTEGER8>(3) AS wrongType,
             a.embedding IS :: VECTOR<FLOAT32>(4) AS wrongDim
      """
    Then the result should be, in any order:
      | d    | same | typed | wrongType | wrongDim |
      | 27.0 | true | true  | false     | false    |
    And no side effects

  Scenario Outline: [6] Invalid vectors are rejected
    Given any graph
    When executing query:
      """
      RETURN <expr> AS v
      """
    Then an ArgumentError should be raised at runtime: InvalidArgumentValue

    Examples:
      | expr                                                                          |
      | vector([1, 2, 3], 2, FLOAT32)                                                 |
      | vector([1, 200], 2, INTEGER8)                                                 |
      | vector([1.5, 2], 2, INTEGER32)                                                |
      | vector_distance(vector([1], 1, FLOAT64), vector([1, 2], 2, FLOAT64), EUCLIDEAN) |
      | vector_distance(vector([1], 1, FLOAT64), vector([1], 1, FLOAT64), MYSTERY)    |

  Scenario: [7] Null propagates
    Given any graph
    When executing query:
      """
      RETURN vector(null, 2, FLOAT32) AS v, vector_dimension_count(null) AS d
      """
    Then the result should be, in any order:
      | v    | d    |
      | null | null |
    And no side effects
