// broken.cc: a file with a syntax error the indexer still reads.
#include <vector>

namespace broken {

class Widget {
 public:
  Widget();
  int size() const { return items_.size(); }

 private:
  std::vector<int> items_;
};

// render is cut short: its body never closes.
void render(const Widget& w {
  if (w.size() > 0) {
    draw(w;
  }

/// after survives the error above.
int after(int x) { return x * 2; }

}  // namespace broken
