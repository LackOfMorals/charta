Feature: TypePredicates - IS :: TYPE and valueType()

  Scenario Outline: [1] Value type predicates
    Given any graph
    When executing query:
      """
      RETURN <value> IS :: <type> AS result
      """
    Then the result should be, in any order:
      | result   |
      | <result> |
    And no side effects

    Examples:
      | value                    | type                          | result |
      | 1                        | INTEGER                       | true   |
      | 1                        | FLOAT                         | false  |
      | 1.5                      | FLOAT                         | true   |
      | 'x'                      | STRING                        | true   |
      | true                     | BOOLEAN                       | true   |
      | 1                        | BOOLEAN                       | false  |
      | null                     | INTEGER                       | true   |
      | null                     | INTEGER NOT NULL              | false  |
      | 1                        | INTEGER NOT NULL              | true   |
      | null                     | NULL                          | true   |
      | 1                        | ANY                           | true   |
      | null                     | ANY                           | true   |
      | null                     | ANY NOT NULL                  | false  |
      | 1                        | INTEGER \| STRING             | true   |
      | 'a'                      | INTEGER \| STRING             | true   |
      | 1.5                      | INTEGER \| STRING             | false  |
      | [1, 2]                   | LIST<INTEGER>                 | true   |
      | [1, 2]                   | LIST<STRING>                  | false  |
      | [1, null]                | LIST<INTEGER NOT NULL>        | false  |
      | [1, null]                | LIST<INTEGER>                 | true   |
      | []                       | LIST<INTEGER>                 | true   |
      | [1, 'a']                 | LIST<INTEGER \| STRING>       | true   |
      | {a: 1}                   | MAP                           | true   |
      | 100                      | INT8                          | true   |
      | 1000                     | INT8                          | false  |
      | date('2020-01-01')       | DATE                          | true   |
      | datetime('2020-01-01T00:00Z') | ZONED DATETIME           | true   |
      | localdatetime('2020-01-01T00:00') | LOCAL DATETIME       | true   |
      | duration('P1D')          | DURATION                      | true   |
      | date('2020-01-01')       | LOCAL DATETIME                | false  |

  Scenario: [2] IS NOT :: negates the predicate
    Given any graph
    When executing query:
      """
      RETURN 1 IS NOT :: STRING AS a, 'x' IS NOT :: STRING AS b
      """
    Then the result should be, in any order:
      | a    | b     |
      | true | false |
    And no side effects

  Scenario: [3] Type predicates on graph entities
    Given an empty graph
    And having executed:
      """
      CREATE (:A)-[:R]->(:B)
      """
    When executing query:
      """
      MATCH p = (a)-[r]->(b)
      RETURN a IS :: NODE AS n, r IS :: RELATIONSHIP AS rel, p IS :: PATH AS path, a IS :: RELATIONSHIP AS wrong
      """
    Then the result should be, in any order:
      | n    | rel  | path | wrong |
      | true | true | true | false |
    And no side effects

  Scenario Outline: [4] valueType reports the most specific type
    Given any graph
    When executing query:
      """
      RETURN valueType(<value>) AS t
      """
    Then the result should be, in any order:
      | t        |
      | <result> |
    And no side effects

    Examples:
      | value                | result                                  |
      | 1                    | 'INTEGER NOT NULL'                      |
      | 1.5                  | 'FLOAT NOT NULL'                        |
      | 'x'                  | 'STRING NOT NULL'                       |
      | true                 | 'BOOLEAN NOT NULL'                      |
      | null                 | 'NULL'                                  |
      | [1, 2]               | 'LIST<INTEGER NOT NULL> NOT NULL'       |
      | [1, null]            | 'LIST<INTEGER> NOT NULL'                |
      | []                   | 'LIST<NOTHING> NOT NULL'                |
      | [1, 'a']             | 'LIST<ANY NOT NULL> NOT NULL'           |
      | {a: 1}               | 'MAP NOT NULL'                          |
      | date('2020-01-01')   | 'DATE NOT NULL'                         |
      | duration('P1D')      | 'DURATION NOT NULL'                     |
