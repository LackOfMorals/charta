Feature: CallSubqueries - CALL { ... } subqueries

  Background:
    Given an empty graph
    And having executed:
      """
      CREATE (a:Person {name: 'Alice'}), (b:Person {name: 'Bob'}), (c:Person {name: 'Carol'}),
             (a)-[:KNOWS]->(b), (a)-[:KNOWS]->(c), (b)-[:KNOWS]->(c)
      """

  Scenario: [1] Importing WITH
    When executing query:
      """
      MATCH (p:Person)
      CALL {
        WITH p
        MATCH (p)-[:KNOWS]->(f)
        RETURN f.name AS friend
      }
      RETURN p.name AS name, friend
      """
    Then the result should be, in any order:
      | name    | friend  |
      | 'Alice' | 'Bob'   |
      | 'Alice' | 'Carol' |
      | 'Bob'   | 'Carol' |
    And no side effects

  Scenario: [2] Variable scope clause
    When executing query:
      """
      MATCH (p:Person)
      CALL (p) {
        MATCH (p)-[:KNOWS]->(f)
        RETURN count(f) AS friends
      }
      RETURN p.name AS name, friends
      """
    Then the result should be, in any order:
      | name    | friends |
      | 'Alice' | 2       |
      | 'Bob'   | 1       |
      | 'Carol' | 0       |
    And no side effects

  Scenario: [3] Empty scope clause imports nothing
    When executing query:
      """
      MATCH (p:Person {name: 'Alice'})
      CALL () {
        MATCH (q:Person) RETURN count(q) AS total
      }
      RETURN p.name AS name, total
      """
    Then the result should be, in any order:
      | name    | total |
      | 'Alice' | 3     |
    And no side effects

  Scenario: [4] OPTIONAL CALL keeps rows without results
    When executing query:
      """
      MATCH (p:Person)
      OPTIONAL CALL (p) {
        MATCH (p)-[:KNOWS]->(f)
        RETURN f.name AS friend
      }
      RETURN p.name AS name, friend
      """
    Then the result should be, in any order:
      | name    | friend  |
      | 'Alice' | 'Bob'   |
      | 'Alice' | 'Carol' |
      | 'Bob'   | 'Carol' |
      | 'Carol' | null    |
    And no side effects

  Scenario: [5] Unit subquery with updates runs for every row
    When executing query:
      """
      MATCH (p:Person)
      CALL (p) {
        CREATE (:Log {who: p.name})
      }
      RETURN count(*) AS rows
      """
    Then the result should be, in any order:
      | rows |
      | 3    |
    And the side effects should be:
      | +nodes      | 3 |
      | +labels     | 1 |
      | +properties | 3 |

  Scenario: [6] IN TRANSACTIONS executes the subquery for every row
    When executing query:
      """
      UNWIND range(1, 5) AS i
      CALL {
        CREATE (:Batch {i: i})
      } IN TRANSACTIONS OF 2 ROWS
      RETURN count(*) AS rows
      """
    Then the result should be, in any order:
      | rows |
      | 5    |
    And the side effects should be:
      | +nodes      | 5 |
      | +labels     | 1 |
      | +properties | 5 |

  Scenario: [7] Subquery with aggregation and a following clause
    When executing query:
      """
      CALL {
        MATCH (p:Person) RETURN p.name AS name ORDER BY name
      }
      RETURN collect(name) AS names
      """
    Then the result should be, in any order:
      | names                     |
      | ['Alice', 'Bob', 'Carol'] |
    And no side effects
