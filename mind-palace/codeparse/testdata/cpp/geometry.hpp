// geometry.hpp: shapes and the registry that owns them.
#pragma once

#include <memory>
#include <string>
#include <vector>
#include "geometry/point.hpp"

#define GEOMETRY_VERSION 3
#define GEOMETRY_SQUARE(x) ((x) * (x))

namespace geometry {

/// Pi, to the precision the shapes need.
constexpr double kPi = 3.14159265358979;

/// The number of shapes created so far.
extern int shapeCount;

/// Unit is the unit a length is measured in.
enum class Unit { Millimetre, Metre };

/// Colour is a shape's fill.
enum Colour {
  Red,
  Green = 2,
  Blue,
};

/// Length is a distance in metres.
using Length = double;

/// Callback is called when a shape changes.
typedef void (*Callback)(int id);

/**
 * Shape is the base of every shape.
 */
class Shape {
 public:
  /// Shape creates a shape with a name.
  explicit Shape(std::string name);
  virtual ~Shape() = default;

  /// Area is the shape's area in square units.
  virtual double area() const = 0;

  /// Name returns the shape's name.
  const std::string& name() const { return name_; }

  bool operator==(const Shape& other) const;

  static int count();

 protected:
  std::string name_;
  Unit unit_ = Unit::Metre;
};

/// Circle is a round shape.
class Circle final : public Shape {
 public:
  Circle(Point centre, Length radius);
  double area() const override;

 private:
  Point centre_;
  Length radius_;
};

/// Bounds is an axis-aligned box.
struct Bounds {
  Point min;
  Point max;

  /// Contains reports whether p lies inside.
  bool contains(const Point& p) const;
};

/// Value holds one of two representations.
union Value {
  int asInt;
  float asFloat;
};

/// Registry owns shapes of one kind.
template <typename T>
class Registry {
 public:
  /// Add stores a shape and returns its index.
  std::size_t add(std::unique_ptr<T> shape);

  T* at(std::size_t i) { return items_.at(i).get(); }

 private:
  std::vector<std::unique_ptr<T>> items_;
};

/// largest returns the shape with the largest area.
template <typename T>
T* largest(const std::vector<T*>& shapes);

/// totalArea sums the areas.
double totalArea(const std::vector<Shape*>& shapes);

}  // namespace geometry
