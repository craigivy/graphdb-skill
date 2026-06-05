class User {
    id;
    name;

    constructor(id, name) {
        this.id = id;
        this.name = name;
    }

    save() {
        console.log("Saving user");
    }
}

module.exports = { User };
