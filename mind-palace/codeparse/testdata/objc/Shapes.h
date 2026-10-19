//
//  Shapes.h
//  ShapeKit
//
//  The public interface of ShapeKit: shapes, the drawing protocol and the
//  C helpers they share.
//

#import <Foundation/Foundation.h>
#import <CoreGraphics/CoreGraphics.h>
#import "ShapeTypes.h"
#include <math.h>

@class Canvas;
@protocol Drawable;

/// The kinds of shape ShapeKit knows.
typedef NS_ENUM(NSInteger, ShapeKind) {
    ShapeKindCircle = 0,
    ShapeKindSquare,
    ShapeKindPolygon,
};

/// A point in three dimensions.
typedef struct {
    double x;
    double y;
    double z;
} SKPoint3;

/// Rendering quality, plain C.
enum SKQuality {
    SKQualityDraft,
    SKQualityFinal
};

typedef union SKValue {
    int asInt;
    double asDouble;
} SKValue;

/// Called when a shape finished drawing.
typedef void (^SKDrawCompletion)(BOOL finished, NSError *error);

typedef CGFloat SKScale;

/// The error domain of ShapeKit.
extern NSString *const SKErrorDomain;

/// The library version.
FOUNDATION_EXPORT double ShapeKitVersionNumber;

#define SK_MAX_SIDES 64
#define SK_CLAMP(v, lo, hi) ((v) < (lo) ? (lo) : ((v) > (hi) ? (hi) : (v)))

/// Returns the distance between two points.
double SKDistance(SKPoint3 a, SKPoint3 b);

/// Makes a shape of a kind.
id<Drawable> SKMakeShape(ShapeKind kind);

/// Anything that can draw itself into a context.
@protocol Drawable <NSObject>

@required
/// Draws the receiver.
- (void)drawInRect:(CGRect)rect context:(CGContextRef)context;

@optional
+ (instancetype)defaultDrawable;
@property (nonatomic, readonly) SKScale scale;

@end

/// A shape with a name and a number of sides.
@interface Shape : NSObject <Drawable, NSCopying> {
  @protected
    NSInteger _sides;
    Canvas *_canvas;
}

@property (nonatomic, copy) NSString *name;
@property (nonatomic, assign, readonly) ShapeKind kind;
@property (nonatomic, copy, nullable) SKDrawCompletion completion;
@property (nonatomic, strong) NSArray<NSString *> *tags;
@property (nonatomic, getter=isFilled) BOOL filled;

/// Creates a shape.
- (instancetype)initWithName:(NSString *)name sides:(NSInteger)sides NS_DESIGNATED_INITIALIZER;
+ (Shape *)shapeNamed:(NSString *)name;
- (double)area;
- (NSArray<Shape *> *)splitInto:(NSUInteger)parts;

@end

/// A generic container of shapes.
@interface ShapeBox<__covariant ObjectType : Shape *> : NSObject <NSFastEnumeration>
- (nullable ObjectType)shapeAtIndex:(NSUInteger)index;
@end

/// Debug helpers, added as a category.
@interface Shape (Debugging)
- (NSString *)debugLabel;
@end
