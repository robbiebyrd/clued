import SwiftUI

/// Nested definitions, result builders, property wrappers and macros.
public struct Canvas: View {
    public struct Layer: Identifiable {
        public enum Blend: Int { case normal, multiply }

        public let id: Int
        var blend: Blend = .normal

        struct Cache {
            static var hits = 0

            /**
             Forgets every hit.
             */
            func reset() { Cache.hits = 0 }
        }
    }

    @State private var layers: [Layer] = []
    public private(set) var revision = 0
    @Environment(\.colorScheme) var scheme

    public var body: some View {
        VStack {
            ForEach(layers) { layer in
                Text("\(layer.id)")
            }
            Button("Add") { addLayer() }
        }
        .padding()
    }

    private func addLayer() {
        func nextID() -> Int { layers.count + 1 }
        let (value, code) = #stringify(nextID())
        layers.append(Layer(id: value))
        print(code)
    }
}

extension Canvas.Layer: Equatable {
    public static func == (a: Self, b: Self) -> Bool { a.id == b.id }
}

public extension Array where Element == Canvas.Layer {
    func topmost() -> Element? { last }
}

@resultBuilder
struct LayerBuilder {
    static func buildBlock(_ layers: Canvas.Layer...) -> [Canvas.Layer] { layers }
}

#Preview {
    Canvas()
}
