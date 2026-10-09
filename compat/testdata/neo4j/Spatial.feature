Feature: Spatial - POINT values

  Scenario Outline: [1] Point accessors
    Given any graph
    When executing query:
      """
      WITH <point> AS p RETURN p.<component> AS c
      """
    Then the result should be, in any order:
      | c        |
      | <result> |
    And no side effects

    Examples:
      | point                                                  | component | result       |
      | point({x: 3, y: 4})                                    | x         | 3.0          |
      | point({x: 3, y: 4})                                    | y         | 4.0          |
      | point({x: 3, y: 4})                                    | z         | null         |
      | point({x: 3, y: 4})                                    | crs       | 'cartesian'  |
      | point({x: 3, y: 4})                                    | srid      | 7203         |
      | point({x: 3, y: 4, z: 5})                              | z         | 5.0          |
      | point({x: 3, y: 4, z: 5})                              | crs       | 'cartesian-3d' |
      | point({longitude: 12.5, latitude: 41.9})               | longitude | 12.5         |
      | point({longitude: 12.5, latitude: 41.9})               | latitude  | 41.9         |
      | point({longitude: 12.5, latitude: 41.9})               | crs       | 'wgs-84'     |
      | point({longitude: 12.5, latitude: 41.9})               | srid      | 4326         |
      | point({longitude: 12.5, latitude: 41.9})               | height    | null         |
      | point({longitude: 12.5, latitude: 41.9, height: 100})  | height    | 100.0        |
      | point({longitude: 12.5, latitude: 41.9, height: 100})  | srid      | 4979         |
      | point({x: 12.5, y: 41.9, crs: 'wgs-84'})               | latitude  | 41.9         |
      | point({x: 1, y: 2, srid: 7203})                        | x         | 1.0          |

  Scenario Outline: [2] Distance between points
    Given any graph
    When executing query:
      """
      RETURN <expr> AS d
      """
    Then the result should be, in any order:
      | d        |
      | <result> |
    And no side effects

    Examples:
      | expr                                                                                                  | result |
      | point.distance(point({x: 0, y: 0}), point({x: 3, y: 4}))                                              | 5.0    |
      | point.distance(point({x: 0, y: 0, z: 0}), point({x: 2, y: 3, z: 6}))                                  | 7.0    |
      | round(point.distance(point({longitude: 0, latitude: 0}), point({longitude: 0, latitude: 1})))         | 111320.0 |
      | point.distance(point({x: 0, y: 0}), point({longitude: 0, latitude: 0}))                               | null   |
      | point.distance(null, point({x: 0, y: 0}))                                                             | null   |

  Scenario Outline: [3] Points within a bounding box
    Given any graph
    When executing query:
      """
      RETURN point.withinBBox(<p>, point({x: 0, y: 0}), point({x: 10, y: 10})) AS w
      """
    Then the result should be, in any order:
      | w        |
      | <result> |
    And no side effects

    Examples:
      | p                      | result |
      | point({x: 5, y: 5})    | true   |
      | point({x: 10, y: 10})  | true   |
      | point({x: 11, y: 5})   | false  |
      | null                   | null   |

  Scenario: [4] Points are stored as properties and compared
    Given an empty graph
    And having executed:
      """
      CREATE (:Place {name: 'a', loc: point({x: 1, y: 2})}), (:Place {name: 'b', loc: point({x: 4, y: 6})})
      """
    When executing query:
      """
      MATCH (a:Place {name: 'a'}), (b:Place {name: 'b'})
      RETURN point.distance(a.loc, b.loc) AS d, a.loc = point({x: 1, y: 2}) AS same, a.loc = b.loc AS differs, a.loc.y AS y
      """
    Then the result should be, in any order:
      | d   | same | differs | y   |
      | 5.0 | true | false   | 2.0 |
    And no side effects

  Scenario: [5] A null coordinate gives a null point
    Given any graph
    When executing query:
      """
      RETURN point({x: null, y: 1}) AS p, point(null) AS q
      """
    Then the result should be, in any order:
      | p    | q    |
      | null | null |
    And no side effects

  Scenario Outline: [6] Invalid points are rejected
    Given any graph
    When executing query:
      """
      RETURN point(<map>) AS p
      """
    Then an ArgumentError should be raised at runtime: InvalidArgumentValue

    Examples:
      | map                                    |
      | {x: 1}                                 |
      | {x: 1, y: 2, longitude: 3}             |
      | {longitude: 1, latitude: 95}           |
      | {x: 1, y: 2, crs: 'nowhere'}           |

  Scenario: [7] Type predicate and valueType
    Given any graph
    When executing query:
      """
      WITH point({x: 1, y: 2}) AS p RETURN p IS :: POINT AS isPoint, valueType(p) AS t
      """
    Then the result should be, in any order:
      | isPoint | t                  |
      | true    | 'POINT NOT NULL'   |
    And no side effects

  Scenario: [8] Points order by coordinate reference system then coordinates
    Given any graph
    When executing query:
      """
      UNWIND [point({x: 2, y: 1}), point({x: 1, y: 5}), point({x: 1, y: 2})] AS p
      RETURN p.x AS x, p.y AS y ORDER BY p
      """
    Then the result should be, in order:
      | x   | y   |
      | 1.0 | 2.0 |
      | 1.0 | 5.0 |
      | 2.0 | 1.0 |
    And no side effects
