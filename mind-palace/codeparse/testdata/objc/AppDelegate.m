@import UIKit;
@import CoreData.NSManagedObject;
#import "AppDelegate.h"
#import "Shapes.h"

@interface AppDelegate () <UITableViewDataSource, CanvasDelegate>
@property (nonatomic, strong) Canvas *canvas;
@property (nonatomic, strong) ShapeBox<Shape *> *box;
@end

@implementation AppDelegate {
    NSUInteger _launchCount;
    __weak id<CanvasDelegate> _forward;
}

- (BOOL)application:(UIApplication *)application didFinishLaunchingWithOptions:(NSDictionary *)launchOptions {
    self.window = [[UIWindow alloc] initWithFrame:[[UIScreen mainScreen] bounds]];
    _launchCount += 1;
    self.canvas = [[Canvas alloc] init];
    self.canvas.delegate = self;
    Shape *square = (Shape *)SKMakeShape(ShapeKindSquare);
    square.completion = ^(BOOL finished, NSError *error) {
        if (!finished) {
            NSLog(@"%@", error.localizedDescription);
        }
    };
    [self.canvas addShape:square];
    __weak typeof(self) weakSelf = self;
    dispatch_async(dispatch_get_main_queue(), ^{
        [weakSelf.canvas setNeedsDisplay];
    });
    Protocol *drawing = @protocol(Drawable);
    if ([square conformsToProtocol:drawing]) {
        [square drawInRect:CGRectZero context:UIGraphicsGetCurrentContext()];
    }
    @try {
        [self.window makeKeyAndVisible];
    } @catch (NSException *exception) {
        NSLog(@"%@", exception.reason);
    }
    return YES;
}

#pragma mark - UITableViewDataSource

- (NSInteger)tableView:(UITableView *)tableView numberOfRowsInSection:(NSInteger)section {
    return (NSInteger)self.canvas.shapes.count;
}

- (UITableViewCell *)tableView:(UITableView *)tableView cellForRowAtIndexPath:(NSIndexPath *)indexPath {
    UITableViewCell *cell = [tableView dequeueReusableCellWithIdentifier:@"Shape" forIndexPath:indexPath];
    Shape *shape = [self.box shapeAtIndex:indexPath.row];
    cell.textLabel.text = shape.debugLabel;
    return cell;
}

#pragma mark - CanvasDelegate

- (void)canvas:(Canvas *)canvas didSelectShape:(Shape *)shape {
    [super canvas:canvas didSelectShape:shape];
}

@end
