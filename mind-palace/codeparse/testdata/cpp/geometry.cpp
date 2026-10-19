// geometry.cpp: the shapes' behaviour.
#include "geometry.hpp"

#include <algorithm>
#include <cmath>
#include <numeric>

#include "util/log.h"

using namespace std;
using std::unique_ptr;
namespace fs = std::filesystem;

namespace geometry {

int shapeCount = 0;

namespace {

// clamp keeps v within [lo, hi].
double clamp(double v, double lo, double hi) {
  return std::max(lo, std::min(v, hi));
}

}  // namespace

Shape::Shape(std::string name) : name_(std::move(name)) {
  ++shapeCount;
  util::log("created", name_);
}

bool Shape::operator==(const Shape& other) const {
  return name_ == other.name_;
}

int Shape::count() { return shapeCount; }

Circle::Circle(Point centre, Length radius)
    : Shape("circle"), centre_(centre), radius_(clamp(radius, 0.0, 1e9)) {}

// area is pi r squared.
double Circle::area() const {
  return kPi * GEOMETRY_SQUARE(radius_);
}

bool Bounds::contains(const Point& p) const {
  return p.x >= min.x && p.x <= max.x && p.y >= min.y && p.y <= max.y;
}

template <typename T>
std::size_t Registry<T>::add(std::unique_ptr<T> shape) {
  items_.push_back(std::move(shape));
  return items_.size() - 1;
}

double totalArea(const std::vector<Shape*>& shapes) {
  return std::accumulate(shapes.begin(), shapes.end(), 0.0,
                         [](double sum, const Shape* s) { return sum + s->area(); });
}

}  // namespace geometry

// geometry::describe prints a shape.
void geometry::describe(const geometry::Shape& s) {
  util::log(s.name(), std::to_string(s.area()));
}

int main(int argc, char** argv) {
  using geometry::Circle;
  Registry<Circle> circles;
  auto c = std::make_unique<Circle>(Point{0, 0}, 2.0);
  circles.add(std::move(c));
  Circle* biggest = largest<Circle>({circles.at(0)});
  geometry::describe(*biggest);
  geometry::Shape* raw = new Circle(Point{1, 1}, 1.0);
  delete raw;
  return 0;
}
