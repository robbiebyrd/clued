// Objective-C++: a C++ namespace and class next to an Objective-C class.
// The Objective-C grammar does not know C++, so the C++ parts parse with
// error nodes; the Objective-C parts still give their symbols.
#import "Bridge.h"
#include <vector>
#include <string>

namespace geometry {

class Mesh {
public:
    explicit Mesh(int vertices) : vertices_(vertices) {}
    int vertexCount() const { return vertices_; }

private:
    int vertices_;
};

} // namespace geometry

/// Wraps a C++ mesh for Objective-C callers.
@interface MeshBridge : NSObject
- (instancetype)initWithVertices:(int)vertices;
- (int)vertexCount;
@end

@implementation MeshBridge {
    geometry::Mesh *_mesh;
}

- (instancetype)initWithVertices:(int)vertices {
    if ((self = [super init])) {
        _mesh = new geometry::Mesh(vertices);
    }
    return self;
}

- (int)vertexCount {
    return _mesh->vertexCount();
}

- (void)dealloc {
    delete _mesh;
}

@end

static int BridgeCount(void) {
    return 1;
}
