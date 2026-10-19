import Foundation

/// Something that can be drawn and measured.
public protocol Shape: AnyObject, CustomStringConvertible {
    /// The unit areas are measured in.
    associatedtype Unit
    typealias Measure = (Double, Unit)

    /// The area, in square units.
    var area: Double { get }
    var name: String { get set }

    /// Draws the shape into a context.
    func draw(in context: DrawingContext) -> Bool
    init(name: String)
    subscript(index: Int) -> Point { get }
}

/// A point on the plane.
public struct Point: Hashable, Codable {
    public let x: Double
    public var y: Double = 0
    static let origin = Point(x: 0, y: 0)
    private var label: String?, tag = 0

    public init(x: Double, y: Double) {
        self.x = x
        self.y = y
    }

    /// Moves the point in place.
    public mutating func move(by dx: Double, _ dy: Double) {
        y += dy
    }

    func distance(to other: Point) -> Double {
        let dx = other.x - x
        return (dx * dx).squareRoot()
    }
}

/// A circle around a centre.
open class Circle: Figure, Shape {
    public typealias Unit = Double

    private var radius: Double
    public var name: String
    public var area: Double { .pi * radius * radius }

    public required init(name: String) {
        self.name = name
        self.radius = 1
    }

    deinit {
        Registry.shared.forget(self)
    }

    public subscript(index: Int) -> Point {
        Point(x: radius, y: Double(index))
    }

    open override func draw(in context: DrawingContext) -> Bool {
        context.stroke(circleAt: .zero, radius: radius)
        return true
    }

    class func unit() -> Circle { Circle(name: "unit") }
}

/// Compass directions.
enum Direction: String, CaseIterable {
    case north, south
    case east = "E"
    case west

    var opposite: Direction {
        switch self {
        case .north: return .south
        case .south: return .north
        case .east: return .west
        case .west: return .east
        }
    }

    func turned() -> Direction { opposite }
}

/// A result that carries a payload.
indirect enum Tree<Value> {
    case leaf(Value)
    case node(Tree, Tree)
}

/// Counts in isolation.
actor Counter {
    private(set) var value = 0

    func increment() -> Int {
        value += 1
        return value
    }
}

extension Point: CustomStringConvertible {
    public var description: String { "(\(x), \(y))" }

    static func + (lhs: Point, rhs: Point) -> Point {
        Point(x: lhs.x + rhs.x, y: lhs.y + rhs.y)
    }
}

/// The default drawing scale.
public let defaultScale = 2.0
var drawCount = 0
public typealias Completion = (Result<Int, Error>) -> Void

/// Draws every shape and counts the successes.
public func drawAll<S: Shape>(_ shapes: [S], in context: DrawingContext) -> Int {
    shapes.filter { $0.draw(in: context) }.count
}

@freestanding(expression)
public macro stringify<T>(_ value: T) -> (T, String) = #externalMacro(module: "ShapeMacros", type: "StringifyMacro")
