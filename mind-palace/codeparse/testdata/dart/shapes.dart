/// Geometry for the demo app: shapes, their areas and a few helpers.
library demo.shapes;

import 'dart:math' as math;

/// The ratio of a circle's circumference to its diameter, to two places.
const double kRoughPi = 3.14;

/// Every shape made so far, by name.
final Map<String, Shape> registry = {};

var created = 0, destroyed = 0;
late String lastName;
int? _cached;

/// Decides whether [value] passes.
typedef Predicate<T> = bool Function(T value);

/// Compares two shapes.
typedef int Compare(Shape a, Shape b);

/// A closed figure in the plane.
abstract class Shape implements Comparable<Shape> {
  /// Creates a shape called [label].
  Shape(this.label) {
    created++;
  }

  /// A shape with no label yet.
  Shape.unnamed() : label = '';

  /// A circle of radius [r].
  factory Shape.circle(double r) => Circle(r);

  /// The shape's name.
  final String label;

  /// How many shapes are alive.
  static int count = 0;

  int _x = 0, _y = 0;

  /// The area, in square units.
  double get area;

  /// Moves the shape to [p].
  set origin(math.Point<int> p) {
    _x = p.x;
    _y = p.y;
  }

  /// Orders shapes by area.
  @override
  int compareTo(Shape other) => area.compareTo(other.area);

  /// Whether two shapes have the same area.
  bool operator ==(Object other) => other is Shape && other.area == area;

  Shape operator +(Shape other);

  /// Scales the shape by [factor].
  Shape scale(double factor);

  static Shape largest(List<Shape> shapes) {
    return shapes.reduce((a, b) => a.compareTo(b) >= 0 ? a : b);
  }

  void _log() => print('$label at $_x,$_y');

  external void release();
}

/// A circle.
class Circle extends Shape with Printable, Comparing<Circle> {
  /// A circle of radius [radius].
  Circle(this.radius) : super('circle');

  /// The unit circle.
  const Circle.unit() : radius = 1, super.unnamed();

  /// A circle read from a map.
  factory Circle.fromMap(Map<String, Object?> map) = _MapCircle;

  final double radius;

  @override
  double get area => math.pi * radius * radius;

  @override
  Shape operator +(Shape other) => Circle(radius + other.area / math.pi);

  @override
  @pragma('vm:prefer-inline')
  Circle scale(double factor) {
    Circle grown(double by) => Circle(radius * by);
    return grown(factor);
  }
}

class _MapCircle extends Circle {
  _MapCircle(Map<String, Object?> map) : super(map['radius'] as double);
}

/// Something that can print itself.
mixin Printable on Shape {
  /// Prints the shape.
  void printMe() => print(describe());

  String describe() => '$label (${area.toStringAsFixed(2)})';
}

mixin Comparing<T extends Shape> implements Comparable<Shape> {
  bool largerThan(T other) => compareTo(other) > 0;
}

/// Shapes in sealed, base, final, interface and mixin classes.
sealed class Polygon extends Shape {
  Polygon(super.label, this.sides);
  final int sides;

  /// Sets the length of side [i].
  void operator []=(int i, double length);
}

final class Square extends Polygon {
  Square(this.side) : super('square', 4);
  final double side;
  @override
  double get area => side * side;
  @override
  Shape operator +(Shape other) => Square(math.sqrt(area + other.area));
  @override
  Square scale(double factor) => Square(side * factor);

  double operator [](int i) => side;

  @override
  void operator []=(int i, double length) => throw UnsupportedError('fixed');

  Square operator -() => Square(-side);

  Square operator ~() => this;
}

base class Anchor {}

interface class Drawable {
  void draw(Canvas canvas) {}
}

mixin class Tagged {
  final tags = <String>{};
}

/// Helpers on every shape list.
extension ShapeList on List<Shape> {
  /// The sum of the areas.
  double get totalArea => fold(0, (sum, s) => sum + s.area);

  List<Shape> sortedByArea() => [...this]..sort();
}

extension on Circle {
  double get diameter => radius * 2;
}

/// A length in metres, with no cost at run time.
extension type const Meters(double value) implements double {
  Meters operator +(Meters other) => Meters(value + other.value);
}

/// The primary colours.
enum Colour { red, green, blue }

/// The planets, with their mass.
enum Planet implements Comparable<Planet> {
  mercury(mass: 0.33),
  earth(mass: 5.97);

  const Planet({required this.mass});

  final double mass;

  bool get isHeavy => mass > 1;

  @override
  int compareTo(Planet other) => mass.compareTo(other.mass);
}

/// The area of every shape, largest first.
List<double> areas(Iterable<Shape> shapes) {
  final sorted = shapes.toList()..sort((a, b) => b.compareTo(a));
  return [for (final s in sorted) s.area];
}

/// The shape most recently made.
Shape? get latest => registry[lastName];

set latest(Shape? shape) {
  if (shape != null) registry[shape.label] = shape;
}

@visibleForTesting
void resetRegistry() => registry.clear();

external int nativeArea(int width, int height);
