import Foundation
import os.log
@testable import ShapeKit
@_exported import Combine
import struct Swift.Array
import class UIKit.UIViewController
import func Darwin.sqrt
import protocol ShapeKit.Shape

#if canImport(UIKit)
import UIKit

typealias PlatformColor = UIColor
#elseif canImport(AppKit)
import AppKit

typealias PlatformColor = NSColor
#endif

let logger = Logger(subsystem: "com.example.shapes", category: "draw")
