Feature: VectorIndex - CREATE VECTOR INDEX and vector validation on write

  Scenario: [1] Create a vector index and show its configuration
    Given an empty graph
    When executing query:
      """
      CREATE VECTOR INDEX doc_embeddings FOR (d:Doc) ON (d.embedding)
      OPTIONS {indexConfig: {`vector.dimensions`: 3, `vector.similarity_function`: 'euclidean'}}
      """
    Then the result should be empty
    And the side effects should be:
      | +indexes | 1 |
    When executing control query:
      """
      SHOW VECTOR INDEXES YIELD name, type, labelsOrTypes, properties, options
      """
    Then the result should be, in any order:
      | name             | type     | labelsOrTypes | properties    | options                                                                                                              |
      | 'doc_embeddings' | 'VECTOR' | ['Doc']       | ['embedding'] | {indexProvider: 'vector-2.0', indexConfig: {`vector.dimensions`: 3, `vector.similarity_function`: 'EUCLIDEAN'}}      |

  Scenario: [2] The similarity function defaults to cosine
    Given an empty graph
    And having executed:
      """
      CREATE VECTOR INDEX v FOR (d:Doc) ON (d.e) OPTIONS {indexConfig: {`vector.dimensions`: 4}}
      """
    When executing query:
      """
      SHOW INDEXES YIELD name, options WHERE name = 'v' RETURN options.indexConfig AS cfg
      """
    Then the result should be, in any order:
      | cfg                                                                       |
      | {`vector.dimensions`: 4, `vector.similarity_function`: 'COSINE'}          |
    And no side effects

  Scenario Outline: [3] Invalid vector index options are rejected
    Given an empty graph
    When executing query:
      """
      CREATE VECTOR INDEX v FOR (d:Doc) ON (d.e) OPTIONS <options>
      """
    Then a SchemaError should be raised at runtime: InvalidOptions

    Examples:
      | options                                                                                                  |
      | {indexConfig: {}}                                                                                        |
      | {indexConfig: {`vector.similarity_function`: 'cosine'}}                                                  |
      | {indexConfig: {`vector.dimensions`: 0}}                                                                  |
      | {indexConfig: {`vector.dimensions`: 5000}}                                                               |
      | {indexConfig: {`vector.dimensions`: 'three'}}                                                            |
      | {indexConfig: {`vector.dimensions`: 3, `vector.similarity_function`: 'manhattan'}}                       |
      | {indexConfig: {`vector.dimensions`: 3, `vector.mystery`: 1}}                                             |
      | {mystery: 1}                                                                                             |

  Scenario: [4] A vector index without options is rejected
    Given an empty graph
    When executing query:
      """
      CREATE VECTOR INDEX v FOR (d:Doc) ON (d.e)
      """
    Then a SchemaError should be raised at runtime: InvalidOptions

  Scenario: [5] Correctly dimensioned vectors and lists are accepted, nulls too
    Given an empty graph
    And having executed:
      """
      CREATE VECTOR INDEX v FOR (d:Doc) ON (d.e) OPTIONS {indexConfig: {`vector.dimensions`: 3}}
      """
    When executing query:
      """
      CREATE (:Doc {e: vector([1, 2, 3], 3, FLOAT32)}), (:Doc {e: [1.5, 2, 3]}), (:Doc {name: 'no vector'}),
             (:Other {e: 'anything'})
      """
    Then the result should be empty
    And the side effects should be:
      | +nodes      | 4 |
      | +labels     | 2 |
      | +properties | 4 |

  Scenario Outline: [6] Wrong vectors on an indexed property are rejected and nothing is written
    Given an empty graph
    And having executed:
      """
      CREATE VECTOR INDEX v FOR (d:Doc) ON (d.e) OPTIONS {indexConfig: {`vector.dimensions`: 3}}
      """
    When executing query:
      """
      CREATE (:Doc {e: <value>})
      """
    Then a ConstraintVerificationFailed should be raised at runtime: ConstraintValidationFailed
    When executing control query:
      """
      MATCH (d:Doc) RETURN count(d) AS c
      """
    Then the result should be, in any order:
      | c |
      | 0 |

    Examples:
      | value                          |
      | [1, 2]                         |
      | [1, 2, 3, 4]                   |
      | [1, 'a', 3]                    |
      | [1, null, 3]                   |
      | vector([1, 2], 2, FLOAT32)     |
      | 'not a vector'                 |
      | 7                              |

  Scenario: [7] Setting the property or adding the label is checked too
    Given an empty graph
    And having executed:
      """
      CREATE VECTOR INDEX v FOR (d:Doc) ON (d.e) OPTIONS {indexConfig: {`vector.dimensions`: 2}}
      """
    And having executed:
      """
      CREATE (:Doc {e: [1, 2]}), (:Thing {e: [1, 2, 3]})
      """
    When executing query:
      """
      MATCH (d:Doc) SET d.e = [9]
      """
    Then a ConstraintVerificationFailed should be raised at runtime: ConstraintValidationFailed
    When executing query:
      """
      MATCH (t:Thing) SET t:Doc
      """
    Then a ConstraintVerificationFailed should be raised at runtime: ConstraintValidationFailed

  Scenario: [8] Vector indexes on relationships
    Given an empty graph
    And having executed:
      """
      CREATE VECTOR INDEX rv FOR ()-[r:SIMILAR]-() ON (r.e) OPTIONS {indexConfig: {`vector.dimensions`: 2}}
      """
    When executing query:
      """
      CREATE (:A)-[:SIMILAR {e: [1, 2, 3]}]->(:B)
      """
    Then a ConstraintVerificationFailed should be raised at runtime: ConstraintValidationFailed

  Scenario: [9] Dropping the index lifts the check
    Given an empty graph
    And having executed:
      """
      CREATE VECTOR INDEX v FOR (d:Doc) ON (d.e) OPTIONS {indexConfig: {`vector.dimensions`: 2}}
      """
    And having executed:
      """
      DROP INDEX v
      """
    When executing query:
      """
      CREATE (:Doc {e: [1, 2, 3]})
      """
    Then the result should be empty
    And the side effects should be:
      | +nodes      | 1 |
      | +labels     | 1 |
      | +properties | 1 |
