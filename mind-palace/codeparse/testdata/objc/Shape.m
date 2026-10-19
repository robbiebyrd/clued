#import "Shapes.h"
#import "Canvas.h"

NSString *const SKErrorDomain = @"com.example.ShapeKit";
double ShapeKitVersionNumber = 1.0;

static NSInteger sShapeCount = 0;

// The private part of Shape.
@interface Shape () <NSSecureCoding>
@property (nonatomic, readwrite) ShapeKind kind;
- (void)invalidate;
@end

@implementation Shape

@synthesize name = _name;
@dynamic filled;

- (instancetype)initWithName:(NSString *)name sides:(NSInteger)sides {
    self = [super init];
    if (self) {
        _name = [name copy];
        _sides = SK_CLAMP(sides, 0, SK_MAX_SIDES);
        sShapeCount++;
    }
    return self;
}

+ (Shape *)shapeNamed:(NSString *)name {
    return [[self alloc] initWithName:name sides:0];
}

- (double)area {
    SKPoint3 origin = {0, 0, 0};
    SKPoint3 corner = {1, 1, 0};
    return SKDistance(origin, corner) * _sides;
}

- (NSArray<Shape *> *)splitInto:(NSUInteger)parts {
    NSMutableArray<Shape *> *result = [NSMutableArray arrayWithCapacity:parts];
    for (NSUInteger i = 0; i < parts; i++) {
        Shape *piece = [Shape shapeNamed:[NSString stringWithFormat:@"%@-%lu", self.name, (unsigned long)i]];
        [result addObject:piece];
    }
    return [result copy];
}

- (void)drawInRect:(CGRect)rect context:(CGContextRef)context {
    CGContextSaveGState(context);
    [_canvas strokeRect:rect width:self.scale];
    CGContextRestoreGState(context);
    if (self.completion) {
        self.completion(YES, nil);
    }
}

- (SKScale)scale {
    return 1.0;
}

- (id)copyWithZone:(NSZone *)zone {
    return [[[self class] allocWithZone:zone] initWithName:self.name sides:_sides];
}

- (void)invalidate {
    [self performSelector:@selector(area) withObject:nil afterDelay:0];
}

+ (BOOL)supportsSecureCoding {
    return YES;
}

@end

@implementation Shape (Debugging)

- (NSString *)debugLabel {
    return [NSString stringWithFormat:@"<%@: %@>", NSStringFromClass([self class]), self.name];
}

@end

double SKDistance(SKPoint3 a, SKPoint3 b) {
    double dx = a.x - b.x, dy = a.y - b.y, dz = a.z - b.z;
    return sqrt(dx * dx + dy * dy + dz * dz);
}

id<Drawable> SKMakeShape(ShapeKind kind) {
    switch (kind) {
    case ShapeKindCircle:
        return [Shape shapeNamed:@"circle"];
    default:
        return [[Shape alloc] initWithName:@"polygon" sides:5];
    }
}

static void SKLogShape(Shape *shape) {
    NSLog(@"%@", [shape debugLabel]);
}
