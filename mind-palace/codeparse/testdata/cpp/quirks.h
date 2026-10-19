// quirks.h: the awkward corners of C++ the indexer still has to read.
#ifndef QUIRKS_H
#define QUIRKS_H

#include <cstdint>
#include <functional>

#ifdef _WIN32
#define QUIRKS_API __declspec(dllexport)
// openHandle opens a handle the Windows way.
int openHandle(const wchar_t* path);
#else
#define QUIRKS_API
// openHandle opens a handle the POSIX way.
int openHandle(const char* path);
#endif

extern "C" {
// quirks_init is the C entry point.
int quirks_init(void);
static const int kQuirksAbi = 2;
}

#define DECLARE_ID(name) \
  struct name##Id {      \
    std::uint64_t value; \
  }

DECLARE_ID(User);

namespace outer::inner {

inline namespace v1 {

/// Packet is a wire frame with bit fields.
struct Packet {
  std::uint8_t version : 4;
  std::uint8_t flags : 4;
  std::uint16_t length;
  char* payload;

  /// Header is nested in Packet.
  struct Header {
    std::uint32_t magic;
    bool valid() const { return magic == 0xC1u; }
  };

  enum class Kind : std::uint8_t { Data, Ack };

  Header header;
  Kind kind = Kind::Data;
};

}  // namespace v1

/// Hash maps a key to a bucket.
template <typename K>
struct Hash {
  std::size_t operator()(const K& key) const;
};

/// Hash<int> is the identity.
template <>
struct Hash<int> {
  std::size_t operator()(int key) const { return static_cast<std::size_t>(key); }
};

/// Matrix is a fixed-size grid.
template <typename T, int Rows, int Cols>
class Matrix : public std::array<T, Rows * Cols>, private outer::Base {
 public:
  /// each calls f on every cell.
  template <typename F>
  void each(F&& f) {
    for (auto& v : *this) f(v);
  }

  T& operator()(int r, int c);

  friend bool operator==(const Matrix& a, const Matrix& b);

 private:
  T** rows_;
};

template <typename T>
using Grid = Matrix<T, 3, 3>;

}  // namespace outer::inner

// The destructor of a nested class, defined outside both.
outer::inner::Packet::Header::~Header() {}

template <typename T, int Rows, int Cols>
T& outer::inner::Matrix<T, Rows, Cols>::operator()(int r, int c) {
  return rows_[r][c];
}

// A qualified template argument makes this runtime read a comparison.
inline void build() {
  outer::inner::Grid<outer::inner::Packet> grid;
  std::function<void(int)> cb = [](int) {};
  cb(openHandle(nullptr));
}

#endif  // QUIRKS_H
