Feature: SchemaCommands - indexes and constraints

  Scenario: [1] Create and show a range index
    Given an empty graph
    When executing query:
      """
      CREATE INDEX person_name FOR (n:Person) ON (n.name)
      """
    Then the result should be empty
    And the side effects should be:
      | +indexes | 1 |
    When executing control query:
      """
      SHOW RANGE INDEXES YIELD name, type, entityType, labelsOrTypes, properties
      """
    Then the result should be, in any order:
      | name          | type    | entityType | labelsOrTypes | properties |
      | 'person_name' | 'RANGE' | 'NODE'     | ['Person']    | ['name']   |

  Scenario: [2] Composite and relationship indexes
    Given an empty graph
    And having executed:
      """
      CREATE INDEX comp FOR (n:Person) ON (n.first, n.last)
      """
    And having executed:
      """
      CREATE INDEX rel_since FOR ()-[r:KNOWS]-() ON (r.since)
      """
    When executing query:
      """
      SHOW INDEXES YIELD name, entityType, properties WHERE name IN ['comp', 'rel_since']
      """
    Then the result should be, in any order:
      | name        | entityType     | properties        |
      | 'comp'      | 'NODE'         | ['first', 'last'] |
      | 'rel_since' | 'RELATIONSHIP' | ['since']         |
    And no side effects

  Scenario: [3] IF NOT EXISTS and duplicate names
    Given an empty graph
    And having executed:
      """
      CREATE INDEX idx FOR (n:A) ON (n.p)
      """
    When executing query:
      """
      CREATE INDEX idx IF NOT EXISTS FOR (n:A) ON (n.p)
      """
    Then the result should be empty
    And no side effects

  Scenario: [4] Creating an index that already exists is an error
    Given an empty graph
    And having executed:
      """
      CREATE INDEX idx FOR (n:A) ON (n.p)
      """
    When executing query:
      """
      CREATE INDEX idx FOR (n:B) ON (n.q)
      """
    Then a SchemaError should be raised at runtime: EquivalentSchemaRuleAlreadyExists

  Scenario: [5] Drop an index
    Given an empty graph
    And having executed:
      """
      CREATE INDEX idx FOR (n:A) ON (n.p)
      """
    When executing query:
      """
      DROP INDEX idx
      """
    Then the result should be empty
    And the side effects should be:
      | -indexes | 1 |
    When executing control query:
      """
      SHOW INDEXES YIELD name WHERE name = 'idx'
      """
    Then the result should be empty

  Scenario: [6] DROP INDEX IF EXISTS on a missing index is a no-op
    Given an empty graph
    When executing query:
      """
      DROP INDEX missing IF EXISTS
      """
    Then the result should be empty
    And no side effects

  Scenario: [7] Dropping a missing index is an error
    Given an empty graph
    When executing query:
      """
      DROP INDEX missing
      """
    Then a SchemaError should be raised at runtime: SchemaRuleNotFound

  Scenario: [8] Uniqueness constraint rejects duplicates
    Given an empty graph
    And having executed:
      """
      CREATE CONSTRAINT person_email FOR (n:Person) REQUIRE n.email IS UNIQUE
      """
    And having executed:
      """
      CREATE (:Person {email: 'a@x.com'})
      """
    When executing query:
      """
      CREATE (:Person {email: 'a@x.com'})
      """
    Then a ConstraintVerificationFailed should be raised at runtime: ConstraintValidationFailed

  Scenario: [9] Uniqueness allows different values, other labels and nulls
    Given an empty graph
    And having executed:
      """
      CREATE CONSTRAINT person_email FOR (n:Person) REQUIRE n.email IS UNIQUE
      """
    When executing query:
      """
      CREATE (:Person {email: 'a@x.com'}), (:Person {email: 'b@x.com'}), (:Dog {email: 'a@x.com'}), (:Person), (:Person)
      """
    Then the result should be empty
    And the side effects should be:
      | +nodes      | 5 |
      | +labels     | 2 |
      | +properties | 3 |

  Scenario: [10] Setting a property to a duplicate violates uniqueness
    Given an empty graph
    And having executed:
      """
      CREATE CONSTRAINT FOR (n:Person) REQUIRE n.email IS UNIQUE
      """
    And having executed:
      """
      CREATE (:Person {email: 'a'}), (:Person {email: 'b'})
      """
    When executing query:
      """
      MATCH (n:Person {email: 'b'}) SET n.email = 'a'
      """
    Then a ConstraintVerificationFailed should be raised at runtime: ConstraintValidationFailed

  Scenario: [11] Two duplicates created by one statement violate uniqueness
    Given an empty graph
    And having executed:
      """
      CREATE CONSTRAINT FOR (n:Person) REQUIRE n.id IS UNIQUE
      """
    When executing query:
      """
      UNWIND [1, 1] AS i CREATE (:Person {id: i})
      """
    Then a ConstraintVerificationFailed should be raised at runtime: ConstraintValidationFailed

  Scenario: [12] A failed statement leaves nothing behind
    Given an empty graph
    And having executed:
      """
      CREATE CONSTRAINT FOR (n:Person) REQUIRE n.id IS UNIQUE
      """
    When executing query:
      """
      UNWIND [1, 2, 1] AS i CREATE (:Person {id: i})
      """
    Then a ConstraintVerificationFailed should be raised at runtime: ConstraintValidationFailed
    When executing control query:
      """
      MATCH (n:Person) RETURN count(n) AS c
      """
    Then the result should be, in any order:
      | c |
      | 0 |

  Scenario: [13] A constraint cannot be created over data that violates it
    Given an empty graph
    And having executed:
      """
      CREATE (:Person {email: 'a'}), (:Person {email: 'a'})
      """
    When executing query:
      """
      CREATE CONSTRAINT FOR (n:Person) REQUIRE n.email IS UNIQUE
      """
    Then a ConstraintVerificationFailed should be raised at runtime: ConstraintValidationFailed

  Scenario: [14] Property existence constraint
    Given an empty graph
    And having executed:
      """
      CREATE CONSTRAINT FOR (n:Person) REQUIRE n.name IS NOT NULL
      """
    When executing query:
      """
      CREATE (:Person {age: 3})
      """
    Then a ConstraintVerificationFailed should be raised at runtime: ConstraintValidationFailed

  Scenario: [15] Removing a required property violates the constraint
    Given an empty graph
    And having executed:
      """
      CREATE CONSTRAINT FOR (n:Person) REQUIRE n.name IS NOT NULL
      """
    And having executed:
      """
      CREATE (:Person {name: 'x'})
      """
    When executing query:
      """
      MATCH (n:Person) REMOVE n.name
      """
    Then a ConstraintVerificationFailed should be raised at runtime: ConstraintValidationFailed

  Scenario: [16] Adding a constrained label to a node checks the constraint
    Given an empty graph
    And having executed:
      """
      CREATE CONSTRAINT FOR (n:Person) REQUIRE n.name IS NOT NULL
      """
    And having executed:
      """
      CREATE (:Thing {id: 1})
      """
    When executing query:
      """
      MATCH (n:Thing) SET n:Person
      """
    Then a ConstraintVerificationFailed should be raised at runtime: ConstraintValidationFailed

  Scenario: [17] Node key constraint requires and deduplicates
    Given an empty graph
    And having executed:
      """
      CREATE CONSTRAINT FOR (n:Person) REQUIRE (n.first, n.last) IS NODE KEY
      """
    And having executed:
      """
      CREATE (:Person {first: 'A', last: 'B'})
      """
    When executing query:
      """
      CREATE (:Person {first: 'A', last: 'C'}), (:Person {first: 'B', last: 'B'})
      """
    Then the result should be empty
    And the side effects should be:
      | +nodes      | 2 |
      | +properties | 4 |

  Scenario: [18] Node key rejects a missing part and a duplicate tuple
    Given an empty graph
    And having executed:
      """
      CREATE CONSTRAINT FOR (n:Person) REQUIRE (n.first, n.last) IS NODE KEY
      """
    And having executed:
      """
      CREATE (:Person {first: 'A', last: 'B'})
      """
    When executing query:
      """
      CREATE (:Person {first: 'A', last: 'B'})
      """
    Then a ConstraintVerificationFailed should be raised at runtime: ConstraintValidationFailed

  Scenario: [19] Property type constraint
    Given an empty graph
    And having executed:
      """
      CREATE CONSTRAINT FOR (n:Person) REQUIRE n.age IS :: INTEGER
      """
    When executing query:
      """
      CREATE (:Person {age: 'old'})
      """
    Then a ConstraintVerificationFailed should be raised at runtime: ConstraintValidationFailed

  Scenario: [20] Property type constraint allows matching types and absent values
    Given an empty graph
    And having executed:
      """
      CREATE CONSTRAINT FOR (n:Person) REQUIRE n.age IS :: INTEGER
      """
    When executing query:
      """
      CREATE (:Person {age: 3}), (:Person {name: 'x'})
      """
    Then the result should be empty
    And the side effects should be:
      | +nodes      | 2 |
      | +labels     | 1 |
      | +properties | 2 |

  Scenario: [21] Relationship constraints
    Given an empty graph
    And having executed:
      """
      CREATE CONSTRAINT FOR ()-[r:RATED]-() REQUIRE r.stars IS NOT NULL
      """
    And having executed:
      """
      CREATE (:U)-[:RATED {stars: 5}]->(:M)
      """
    When executing query:
      """
      MATCH (u:U), (m:M) CREATE (u)-[:RATED]->(m)
      """
    Then a ConstraintVerificationFailed should be raised at runtime: ConstraintValidationFailed

  Scenario: [22] Show and drop constraints
    Given an empty graph
    And having executed:
      """
      CREATE CONSTRAINT c_unique FOR (n:Person) REQUIRE n.email IS UNIQUE
      """
    And having executed:
      """
      CREATE CONSTRAINT c_exists FOR (n:Person) REQUIRE n.name IS NOT NULL
      """
    When executing query:
      """
      SHOW CONSTRAINTS YIELD name, type, labelsOrTypes, properties
      """
    Then the result should be, in any order:
      | name       | type                      | labelsOrTypes | properties |
      | 'c_unique' | 'UNIQUENESS'              | ['Person']    | ['email']  |
      | 'c_exists' | 'NODE_PROPERTY_EXISTENCE' | ['Person']    | ['name']   |
    And no side effects
    When executing query:
      """
      DROP CONSTRAINT c_unique
      """
    Then the result should be empty
    And the side effects should be:
      | -constraints | 1 |

  Scenario: [23] Dropping a constraint lifts the restriction
    Given an empty graph
    And having executed:
      """
      CREATE CONSTRAINT c FOR (n:Person) REQUIRE n.id IS UNIQUE
      """
    And having executed:
      """
      DROP CONSTRAINT c
      """
    When executing query:
      """
      CREATE (:Person {id: 1}), (:Person {id: 1})
      """
    Then the result should be empty
    And the side effects should be:
      | +nodes      | 2 |
      | +labels     | 1 |
      | +properties | 2 |

  Scenario: [24] A uniqueness constraint owns a lookup index shown by SHOW INDEXES
    Given an empty graph
    And having executed:
      """
      CREATE CONSTRAINT c FOR (n:Person) REQUIRE n.id IS UNIQUE
      """
    When executing query:
      """
      SHOW INDEXES YIELD name, owningConstraint WHERE owningConstraint IS NOT NULL
      """
    Then the result should be, in any order:
      | name | owningConstraint |
      | 'c'  | 'c'              |
    And no side effects

  Scenario: [25] SHOW PROCEDURES lists the built-ins
    Given an empty graph
    When executing query:
      """
      SHOW PROCEDURES YIELD name WHERE name STARTS WITH 'db.' RETURN name
      """
    Then the result should be, in any order:
      | name                   |
      | 'db.index.vector.queryNodes' |
      | 'db.labels'            |
      | 'db.propertyKeys'      |
      | 'db.relationshipTypes' |
