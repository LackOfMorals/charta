Feature: DynamicLabelsAndProperties - labels and property keys computed at run time

  Scenario: [1] Match with a dynamic label
    Given an empty graph
    And having executed:
      """
      CREATE (:Person {name: 'Alice'}), (:Dog {name: 'Rex'})
      """
    When executing query:
      """
      WITH 'Person' AS label
      MATCH (n:$(label)) RETURN n.name AS name
      """
    Then the result should be, in any order:
      | name    |
      | 'Alice' |
    And no side effects

  Scenario: [2] Create a node with dynamic labels from a list
    Given an empty graph
    When executing query:
      """
      CREATE (n:$(['A', 'B'])) RETURN labels(n) AS l
      """
    Then the result should be, in any order:
      | l          |
      | ['A', 'B'] |
    And the side effects should be:
      | +nodes  | 1 |
      | +labels | 2 |

  Scenario: [3] Set and remove dynamic labels
    Given an empty graph
    And having executed:
      """
      CREATE (:Old {id: 1})
      """
    When executing query:
      """
      MATCH (n {id: 1}) SET n:$('New') REMOVE n:$('Old') RETURN labels(n) AS l
      """
    Then the result should be, in any order:
      | l       |
      | ['New'] |
    And the side effects should be:
      | +labels | 1 |
      | -labels | 1 |

  Scenario: [4] Read a property with a dynamic key
    Given an empty graph
    And having executed:
      """
      CREATE ({name: 'Alice', age: 30})
      """
    When executing query:
      """
      WITH 'age' AS key MATCH (n) RETURN n[key] AS v
      """
    Then the result should be, in any order:
      | v  |
      | 30 |
    And no side effects

  Scenario: [5] Set a property with a dynamic key
    Given an empty graph
    And having executed:
      """
      CREATE ({id: 1})
      """
    When executing query:
      """
      WITH 'colour' AS key MATCH (n {id: 1}) SET n[key] = 'red' RETURN n.colour AS c
      """
    Then the result should be, in any order:
      | c     |
      | 'red' |
    And the side effects should be:
      | +properties | 1 |

  Scenario: [6] A dynamic relationship type
    Given an empty graph
    And having executed:
      """
      CREATE (:X {id: 1}), (:X {id: 2})
      """
    When executing query:
      """
      MATCH (a:X {id: 1}), (b:X {id: 2}) CREATE (a)-[r:$('LINKS')]->(b) RETURN type(r) AS t
      """
    Then the result should be, in any order:
      | t       |
      | 'LINKS' |
    And the side effects should be:
      | +relationships | 1 |
