#import "Shapes.h"

@interface Broken : NSObject
- (void)ok;
@end

@implementation Broken

- (void)ok {
    [self helper:
}

- (void)helper:(id)value {
    NSLog(@"%@", value);
}

@end

int BrokenAfter(int x) {
    return x + 1;
}
