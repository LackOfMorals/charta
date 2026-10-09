Feature: StringFunctions - Cypher 25 string functions and forms

  Scenario Outline: [1] Trim forms
    Given any graph
    When executing query:
      """
      RETURN <expr> AS r
      """
    Then the result should be, in any order:
      | r        |
      | <result> |
    And no side effects

    Examples:
      | expr                                  | result  |
      | trim('  ab  ')                        | 'ab'    |
      | btrim('xxabxx', 'x')                  | 'ab'    |
      | ltrim('xxabxx', 'x')                  | 'abxx'  |
      | rtrim('xxabxx', 'x')                  | 'xxab'  |
      | trim(BOTH 'x' FROM 'xxabxx')          | 'ab'    |
      | trim(LEADING 'x' FROM 'xxabxx')       | 'abxx'  |
      | trim(TRAILING 'x' FROM 'xxabxx')      | 'xxab'  |
      | trim(BOTH FROM '  ab  ')              | 'ab'    |
      | trim('x' FROM 'xxabxx')               | 'ab'    |
      | trim(null)                            | null    |

  Scenario Outline: [2] Normalization
    Given any graph
    When executing query:
      """
      RETURN <expr> AS r
      """
    Then the result should be, in any order:
      | r        |
      | <result> |
    And no side effects

    Examples:
      | expr                                    | result |
      | size(normalize('é', NFD))               | 2      |
      | size(normalize('é'))              | 1      |
      | 'é' IS NORMALIZED                       | true   |
      | 'é' IS NOT NORMALIZED             | true   |
      | normalize(null)                         | null   |

  Scenario Outline: [3] Hyperbolic functions and isNaN
    Given any graph
    When executing query:
      """
      RETURN <expr> AS r
      """
    Then the result should be, in any order:
      | r        |
      | <result> |
    And no side effects

    Examples:
      | expr             | result |
      | sinh(0)          | 0.0    |
      | cosh(0)          | 1.0    |
      | tanh(0)          | 0.0    |
      | isNaN(0.0 / 0.0) | true   |
      | isNaN(1.5)       | false  |
      | isNaN(null)      | null   |
