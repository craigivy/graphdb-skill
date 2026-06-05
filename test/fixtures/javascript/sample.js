const { User: UserAlias } = require('./models/User');

function hello(name) {
    console.log("Hello, " + name);
}

class Greeter {
    greet() { return "Hi"; }
}

class SuperUser extends UserAlias {
    role;
    constructor(id, name, role) {
        super(id, name);
        this.role = role;
    }
}

function main() {
    hello("world");
    const g = new Greeter();
    g.greet();
    
    const u = new UserAlias("1", "Alice");
    u.save();
}
