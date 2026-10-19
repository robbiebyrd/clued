import Foundation

struct Valid {
    let name: String
    func greet() -> String { "hello \(name)" }
}

class Broken: Valid {
    func half( -> Int {
        return 1 +
    }

    var dangling: = 
}

func afterwards() -> Int {
    Valid(name: "x").greet().count
}
