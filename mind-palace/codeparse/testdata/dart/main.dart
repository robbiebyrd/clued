void main(List<String> args) => print(greet(args.isEmpty ? 'world' : args.first));

/// Greets [name].
@pragma('vm:prefer-inline')
String greet(String name) => 'Hello, ${name.trim()}!';
