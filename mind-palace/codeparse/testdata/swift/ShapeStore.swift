import Combine
import Foundation
import ShapeKit

/// Errors the store reports.
enum StoreError: Error {
    case notFound(id: UUID)
    case invalid(String)
}

protocol ShapeRepository {
    func load(id: UUID) async throws -> Circle
    func save(_ shape: Circle) async throws
}

/// Loads and caches shapes for the editor.
@MainActor
final class ShapeStore: ObservableObject, ShapeRepository {
    @Published private(set) var shapes: [UUID: Circle] = [:]
    private let backend: Backend
    private var cancellables = Set<AnyCancellable>()

    init(backend: Backend = .live) {
        self.backend = backend
        backend.changes
            .receive(on: DispatchQueue.main)
            .sink { [weak self] change in self?.apply(change) }
            .store(in: &cancellables)
    }

    func load(id: UUID) async throws -> Circle {
        if let cached = shapes[id] {
            return cached
        }
        guard let data = try await backend.fetch(id) else {
            throw StoreError.notFound(id: id)
        }
        let circle = try JSONDecoder().decode(Circle.self, from: data)
        shapes[id] = circle
        return circle
    }

    func save(_ shape: Circle) async throws {
        let data = try JSONEncoder().encode(shape)
        try await backend.put(data, for: shape.id)
        notify(ShapeEvent.saved(shape.id))
    }

    private func apply(_ change: Backend.Change) {
        switch change {
        case .removed(let id):
            shapes.removeValue(forKey: id)
        case .updated(let circle):
            shapes[circle.id] = circle
        }
    }
}

final class CachingStore<Key: Hashable, Value>: Cache<Key, Value>, Sendable {
    override func value(for key: Key) -> Value? {
        super.value(for: key) ?? fallback(key)
    }
}

func notify(_ event: ShapeEvent) {
    NotificationCenter.default.post(name: .shapeEvent, object: event)
    Analytics.track(event.name)
}
